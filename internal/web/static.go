package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// staticAsset es un fichero estático precargado en memoria (y comprimido si compensa).
type staticAsset struct {
	raw, gz []byte
	ctype   string
	hash    string // primeros 10 caracteres del sha256, para versionar la URL
}

// loadStatic lee todos los estáticos embebidos, calcula su hash y los comprime con gzip.
func loadStatic(fsys fs.FS) (map[string]*staticAsset, error) {
	assets := map[string]*staticAsset{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		a := &staticAsset{raw: b, hash: hex.EncodeToString(sum[:])[:10], ctype: mime.TypeByExtension(path.Ext(p))}
		if a.ctype == "" {
			a.ctype = http.DetectContentType(b)
		}
		if len(b) > 1024 {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			zw.Write(b)
			zw.Close()
			if buf.Len() < len(b) {
				a.gz = buf.Bytes()
			}
		}
		assets[p] = a
		return nil
	})
	return assets, err
}

// assetURL devuelve la URL versionada de un estático (/static/app.css?v=<hash>).
func (s *Server) assetURL(name string) string {
	if a, ok := s.static[name]; ok {
		return "/static/" + name + "?v=" + a.hash
	}
	return "/static/" + name
}

// serveStatic sirve los estáticos: con ?v= coincidente se cachean un año (immutable);
// sin versión, el navegador revalida con ETag.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	a, ok := s.static[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	etag := `"` + a.hash + `"`
	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("ETag", etag)
	h.Set("Vary", "Accept-Encoding")
	if r.URL.Query().Get("v") == a.hash {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := a.raw
	if a.gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		body = a.gz
	}
	w.Write(body)
}
