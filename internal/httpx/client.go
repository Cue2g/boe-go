// Package httpx es el cliente HTTP estandarizado que usan todas las fuentes:
// headers de navegador, cookies, decodificación de charset, reintentos y pausas.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/html/charset"
)

// Request describe una petición de forma uniforme para cualquier fuente.
type Request struct {
	Method  string // GET por defecto; POST si Form != nil y Method vacío
	URL     string
	Query   url.Values // se añade a la query de URL
	Form    url.Values // cuerpo application/x-www-form-urlencoded
	Headers map[string]string
}

// Options configura un Client.
type Options struct {
	MinDelay, MaxDelay time.Duration // pausa aleatoria en Pace()
	Timeout            time.Duration // por petición (default 60s)
	Retries            int           // reintentos en error de red / 5xx (default 3)
	DumpDir            string        // si no está vacío, guarda cada respuesta HTML
	Name               string        // prefijo de los ficheros volcados / logs
}

// Client es un cliente HTTP con cookie jar propio.
type Client struct {
	http *http.Client
	opts Options
	n    atomic.Int64
}

var defaultHeaders = map[string]string{
	"User-Agent":      "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
	"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
	"Accept-Language": "es-ES,es;q=0.9,en;q=0.8",
}

// New crea un Client.
func New(o Options) *Client {
	if o.Timeout == 0 {
		o.Timeout = 60 * time.Second
	}
	if o.Retries == 0 {
		o.Retries = 3
	}
	jar, _ := cookiejar.New(nil)
	return &Client{http: &http.Client{Jar: jar, Timeout: o.Timeout}, opts: o}
}

// Pace duerme un tiempo aleatorio entre MinDelay y MaxDelay (o hasta que se cancele ctx).
func (c *Client) Pace(ctx context.Context) error {
	d := c.opts.MinDelay
	if span := c.opts.MaxDelay - c.opts.MinDelay; span > 0 {
		d += time.Duration(rand.Int64N(int64(span)))
	}
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Cookies devuelve las cookies guardadas para rawURL.
func (c *Client) Cookies(rawURL string) []*http.Cookie {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	return c.http.Jar.Cookies(u)
}

// Do ejecuta la petición y devuelve el cuerpo decodificado a UTF-8.
func (c *Client) Do(ctx context.Context, r Request) (string, error) {
	var lastErr error
	for attempt := 0; attempt <= c.opts.Retries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * 2 * time.Second
			slog.Warn("reintentando", "source", c.opts.Name, "url", r.URL, "attempt", attempt, "err", lastErr)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
		}
		body, retry, err := c.do(ctx, r)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return "", lastErr
}

func (c *Client) do(ctx context.Context, r Request) (body string, retry bool, err error) {
	u, err := url.Parse(r.URL)
	if err != nil {
		return "", false, err
	}
	if len(r.Query) > 0 {
		q := u.Query()
		for k, vs := range r.Query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}

	method := r.Method
	if method == "" {
		method = http.MethodGet
		if r.Form != nil {
			method = http.MethodPost
		}
	}
	var rd io.Reader
	if r.Form != nil {
		rd = strings.NewReader(r.Form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return "", false, err
	}
	for k, v := range defaultHeaders {
		req.Header.Set(k, v)
	}
	if r.Form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", true, fmt.Errorf("%s %s: %w", method, u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return "", true, fmt.Errorf("%s %s: status %d", method, u, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return "", false, fmt.Errorf("%s %s: status %d", method, u, resp.StatusCode)
	}

	utf8Reader, err := charset.NewReader(resp.Body, resp.Header.Get("Content-Type"))
	if err != nil {
		return "", false, fmt.Errorf("charset: %w", err)
	}
	b, err := io.ReadAll(utf8Reader)
	if err != nil {
		return "", true, fmt.Errorf("leyendo cuerpo de %s: %w", u, err)
	}
	c.dump(u, b)
	return string(b), false, nil
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func (c *Client) dump(u *url.URL, b []byte) {
	if c.opts.DumpDir == "" {
		return
	}
	n := c.n.Add(1)
	name := fmt.Sprintf("%s_%04d_%s.html", c.opts.Name, n, unsafeChars.ReplaceAllString(u.Path, "_"))
	if err := os.MkdirAll(c.opts.DumpDir, 0o755); err != nil {
		slog.Error("dump", "err", err)
		return
	}
	if err := os.WriteFile(filepath.Join(c.opts.DumpDir, name), b, 0o644); err != nil {
		slog.Error("dump", "err", err)
	}
}

// ErrEmpty se usa cuando una fuente no devuelve resultados.
var ErrEmpty = errors.New("la fuente no devolvió resultados")
