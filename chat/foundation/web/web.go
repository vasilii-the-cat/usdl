// Package web contains a small web framework extension.
package web

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
)

// Encoder defines behavior that can encode a data model and provide
// the content type for that encoding.
type Encoder interface {
	Encode() (data []byte, contentType string, err error)
}

// HandlerFunc represents a function that handles a http request within our own
// little mini framework.
type HandlerFunc func(ctx context.Context, r *http.Request) Encoder

// Logger represents a function that will be called to add information
// to the logs.
type Logger func(ctx context.Context, msg string, args ...any)

// App is the entrypoint into our application and what configures our context
// object for each of our http handlers. Feel free to add any configuration
// data/logic on this App struct.
type App struct {
	log     Logger
	mux     *http.ServeMux
	mw      []MidFunc
	origins []string
}

// NewApp creates an App value that handle a set of routes for the application.
func NewApp(log Logger, mw ...MidFunc) *App {
	return &App{
		log: log,
		mux: http.NewServeMux(),
		mw:  mw,
	}
}

// ServeHTTP implements the http.Handler interface. It's the entry point for
// all http traffic and allows the opentelemetry mux to run first to handle
// tracing. The opentelemetry mux then calls the application mux to handle
// application traffic. This was set up in the NewApp function.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mux.ServeHTTP(w, r)
}

// EnableCORS enables CORS preflight requests to work. It prevents the
// MethodNotAllowedHandler from being called.
func (a *App) EnableCORS(origins []string) {
	a.origins = origins
}

// HandlerFuncNoMid sets a handler function for a given HTTP method and path
// pair to the application server mux. Does not include the application
// middleware.
func (a *App) HandlerFuncNoMid(method string, group string, path string, handlerFunc HandlerFunc) {
	h := func(w http.ResponseWriter, r *http.Request) {
		ctx := setWriter(r.Context(), w)

		resp := handlerFunc(ctx, r)

		if err := Respond(ctx, w, resp); err != nil {
			a.log(ctx, "web-respond", "ERROR", err)
			return
		}
	}

	finalPath := path
	if group != "" {
		finalPath = "/" + group + path
	}
	finalPath = fmt.Sprintf("%s %s", method, finalPath)

	a.mux.HandleFunc(finalPath, h)
}

// HandlerFunc sets a handler function for a given HTTP method and path pair
// to the application server mux.
func (a *App) HandlerFunc(method string, group string, path string, handlerFunc HandlerFunc, mw ...MidFunc) {
	handlerFunc = wrapMiddleware(mw, handlerFunc)
	handlerFunc = wrapMiddleware(a.mw, handlerFunc)

	h := func(w http.ResponseWriter, r *http.Request) {
		ctx := setWriter(r.Context(), w)
		ctx = setWriter(ctx, w)

		resp := handlerFunc(ctx, r)

		if err := Respond(ctx, w, resp); err != nil {
			a.log(ctx, "web-respond", "ERROR", err)
			return
		}
	}

	finalPath := path
	if group != "" {
		finalPath = "/" + group + path
	}
	finalPath = fmt.Sprintf("%s %s", method, finalPath)

	a.mux.HandleFunc(finalPath, h)
}

// RawHandlerFunc sets a raw handler function for a given HTTP method and path
// pair to the application server mux.
func (a *App) RawHandlerFunc(method string, group string, path string, rawHandlerFunc http.HandlerFunc, mw ...MidFunc) {
	handlerFunc := func(ctx context.Context, r *http.Request) Encoder {
		r = r.WithContext(ctx)
		rawHandlerFunc(GetWriter(ctx), r)
		return nil
	}

	handlerFunc = wrapMiddleware(mw, handlerFunc)
	handlerFunc = wrapMiddleware(a.mw, handlerFunc)

	h := func(w http.ResponseWriter, r *http.Request) {
		ctx := setWriter(r.Context(), w)
		ctx = setWriter(ctx, w)

		handlerFunc(ctx, r)
	}

	finalPath := path
	if group != "" {
		finalPath = "/" + group + path
	}
	finalPath = fmt.Sprintf("%s %s", method, finalPath)

	a.mux.HandleFunc(finalPath, h)
}

// FileServerReact starts a file server based on the specified file system and
// directory inside that file system for a statically built react webapp.
func (a *App) FileServerReact(static embed.FS, dir string, path string) error {
	fileMatcher := regexp.MustCompile(`\.[a-zA-Z]*$`)

	fSys, err := fs.Sub(static, dir)
	if err != nil {
		return fmt.Errorf("switching to static folder: %w", err)
	}

	fileServer := http.StripPrefix(path, http.FileServer(http.FS(fSys)))

	h := func(w http.ResponseWriter, r *http.Request) {
		if !fileMatcher.MatchString(r.URL.Path) {
			p, err := static.ReadFile(fmt.Sprintf("%s/index.html", dir))
			if err != nil {
				a.log(r.Context(), "FileServerReact", "index.html not found", "ERROR", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(p)
			return
		}

		fileServer.ServeHTTP(w, r)
	}

	a.mux.HandleFunc(fmt.Sprintf("GET %s", path), h)

	return nil
}

// FileServer starts a file server based on the specified file system and
// directory inside that file system.
func (a *App) FileServer(static embed.FS, dir string, path string) error {
	fSys, err := fs.Sub(static, dir)
	if err != nil {
		return fmt.Errorf("switching to static folder: %w", err)
	}

	fileServer := http.StripPrefix(path, http.FileServer(http.FS(fSys)))

	a.mux.Handle(fmt.Sprintf("GET %s", path), fileServer)

	return nil
}
