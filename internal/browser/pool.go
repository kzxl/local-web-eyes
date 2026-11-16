package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/local-web-eyes/local-web-eyes/internal/config"
	"github.com/local-web-eyes/local-web-eyes/internal/security"
)

var ErrBrowserDisabled = errors.New("headless browser is disabled or unavailable")

// Pool manages a lazy, isolated Chromium/Edge instance over CDP with strict
// SSRF request hijacking and zero file downloads.
type Pool struct {
	cfg        config.BrowserConfig
	guard      *security.Guard
	httpClient *http.Client

	mu      sync.Mutex
	browser *rod.Browser
	router  *rod.HijackRouter
	tempDir string
	sem     chan struct{}
	closed  bool
}

// NewPool creates a lazy browser pool (no browser process is launched until needed).
func NewPool(cfg config.BrowserConfig, guard *security.Guard) *Pool {
	maxConc := cfg.MaxConcurrentPages
	if maxConc <= 0 {
		maxConc = 2
	}
	timeout := cfg.PageTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	var httpClient *http.Client
	if guard != nil {
		httpClient = guard.NewHTTPClient(timeout)
	}
	return &Pool{
		cfg:        cfg,
		guard:      guard,
		httpClient: httpClient,
		sem:        make(chan struct{}, maxConc),
	}
}

// Available returns true if browser rendering is enabled and an executable exists.
func (p *Pool) Available() bool {
	if p == nil || !p.cfg.Enabled {
		return false
	}
	return p.findExecutable() != ""
}

// RenderHTML navigates to targetURL in an isolated incognito page and returns rendered DOM HTML.
func (p *Pool) RenderHTML(ctx context.Context, targetURL string) (string, error) {
	return p.RenderHTMLWithWait(ctx, targetURL, "")
}

// RenderHTMLWithWait navigates to targetURL, optionally waits for a CSS selector, and returns HTML.
func (p *Pool) RenderHTMLWithWait(ctx context.Context, targetURL, waitSelector string) (string, error) {
	if p == nil || !p.cfg.Enabled {
		return "", ErrBrowserDisabled
	}
	if _, err := p.guard.ValidateURL(targetURL); err != nil {
		return "", err
	}

	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	b, err := p.ensureBrowser()
	if err != nil {
		return "", err
	}

	timeout := p.cfg.PageTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create an isolated incognito context per navigation so cookies/storage never persist.
	incog, err := b.Incognito()
	if err != nil {
		p.resetBrowser(b)
		b, err = p.ensureBrowser()
		if err != nil {
			return "", err
		}
		incog, err = b.Incognito()
		if err != nil {
			return "", fmt.Errorf("create incognito context: %w", err)
		}
	}
	defer func() { _ = incog.Close() }()

	page, err := incog.Context(runCtx).Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return "", fmt.Errorf("open blank page: %w", err)
	}
	defer func() { _ = page.Close() }()

	if err := page.Navigate(targetURL); err != nil {
		return "", fmt.Errorf("navigate %s: %w", targetURL, err)
	}
	_ = page.WaitLoad()
	_ = page.WaitIdle(1500 * time.Millisecond)

	if strings.TrimSpace(waitSelector) != "" {
		_, _ = page.Timeout(4 * time.Second).Element(waitSelector)
	}

	htmlOut, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("read rendered DOM: %w", err)
	}
	return htmlOut, nil
}

// Close shuts down the browser process and deletes the temporary user-data directory.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return p.cleanupLocked()
}

func (p *Pool) resetBrowser(failed *rod.Browser) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.browser != failed {
		return
	}
	_ = p.cleanupLocked()
}

func (p *Pool) cleanupLocked() error {
	if p.router != nil {
		_ = p.router.Stop()
		p.router = nil
	}
	var closeErr error
	if p.browser != nil {
		closeErr = p.browser.Close()
		p.browser = nil
	}
	if p.tempDir != "" {
		_ = os.RemoveAll(p.tempDir)
		p.tempDir = ""
	}
	return closeErr
}

func (p *Pool) ensureBrowser() (*rod.Browser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, errors.New("browser pool closed")
	}
	if p.browser != nil {
		return p.browser, nil
	}

	bin := p.findExecutable()
	if bin == "" {
		return nil, fmt.Errorf("%w: no Chrome/Edge executable found", ErrBrowserDisabled)
	}

	tmpDir, err := os.MkdirTemp("", "lwe-browser-*")
	if err != nil {
		return nil, fmt.Errorf("create temp profile dir: %w", err)
	}

	l := launcher.New().
		Bin(bin).
		Headless(true).
		UserDataDir(tmpDir).
		Set("no-first-run").
		Set("no-default-browser-check").
		Set("disable-extensions").
		Set("disable-background-networking").
		Set("disable-sync").
		Set("mute-audio").
		Set("disable-gpu")

	if runtime.GOOS == "linux" {
		l = l.Set("no-sandbox")
	}

	controlURL, err := l.Launch()
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("launch browser (%s): %w", filepath.Base(bin), err)
	}

	b := rod.New().ControlURL(controlURL)
	if err := b.Connect(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("connect CDP: %w", err)
	}

	// Deny all file downloads at browser level.
	_ = proto.BrowserSetDownloadBehavior{
		Behavior: proto.BrowserSetDownloadBehaviorBehaviorDeny,
	}.Call(b)

	router := b.HijackRequests()
	_ = router.Add("*", "", func(h *rod.Hijack) {
		p.handleHijack(h)
	})
	go router.Run()

	p.browser = b
	p.router = router
	p.tempDir = tmpDir
	return p.browser, nil
}

func (p *Pool) handleHijack(h *rod.Hijack) {
	reqURL := h.Request.URL()
	if reqURL == nil {
		h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
		return
	}

	// Drop heavy non-text assets to minimize RAM and latency.
	if p.cfg.BlockMedia {
		switch h.Request.Type() {
		case proto.NetworkResourceTypeImage,
			proto.NetworkResourceTypeMedia,
			proto.NetworkResourceTypeFont:
			h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}
	}

	if p.guard != nil {
		if _, err := p.guard.ValidateURL(reqURL.String()); err != nil {
			h.Response.Fail(proto.NetworkErrorReasonAccessDenied)
			return
		}
		checkCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := p.guard.CheckHostIPs(checkCtx, reqURL.Hostname()); err != nil {
			h.Response.Fail(proto.NetworkErrorReasonAccessDenied)
			return
		}
	}

	if p.httpClient != nil {
		h.Request.Req().Header.Del("Accept-Encoding")
		if err := h.LoadResponse(p.httpClient, true); err != nil {
			if errors.Is(err, security.ErrSSRFBlocked) || errors.Is(err, security.ErrBlocklistMatch) {
				h.Response.Fail(proto.NetworkErrorReasonAccessDenied)
			} else {
				h.Response.Fail(proto.NetworkErrorReasonFailed)
			}
			return
		}
		return
	}

	h.ContinueRequest(&proto.FetchContinueRequest{})
}

func (p *Pool) findExecutable() string {
	if strings.TrimSpace(p.cfg.BinaryPath) != "" {
		if _, err := os.Stat(p.cfg.BinaryPath); err == nil {
			return p.cfg.BinaryPath
		}
	}
	if found, ok := launcher.LookPath(); ok {
		return found
	}
	candidates := []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
