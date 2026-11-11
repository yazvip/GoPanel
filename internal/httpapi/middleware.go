package httpapi

import (
	"bufio"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				s.logger.Error("panic while serving request", "method", r.Method, "path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
				if rr, ok := w.(*statusRecorder); !ok || !rr.wrote {
					s.writeError(w, errf(http.StatusInternalServerError, "internal_error", "internal server error"))
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasPrefix(r.URL.Path, "/vendor/") || strings.HasPrefix(r.URL.Path, "/locales/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rr, r)
		s.logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rr.status,
			"bytes", rr.bytes,
			"ip", s.clientIP(r),
			"duration", time.Since(start).Round(time.Millisecond).String(),
		)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; media-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if r.URL.Path != APIPrefix+"/terminal/ws" {
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(data)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Flush() {
	_ = http.NewResponseController(r.ResponseWriter).Flush()
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(r.ResponseWriter).Hijack()
}

func (r *statusRecorder) SetReadDeadline(deadline time.Time) error {
	return http.NewResponseController(r.ResponseWriter).SetReadDeadline(deadline)
}

func (r *statusRecorder) SetWriteDeadline(deadline time.Time) error {
	return http.NewResponseController(r.ResponseWriter).SetWriteDeadline(deadline)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
