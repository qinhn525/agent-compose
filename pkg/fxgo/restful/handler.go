package restful

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
)

func NullHandler[RT ResponseType[any]](w http.ResponseWriter, r *http.Request) {
	// Get the directory of the executable
	ex, err := os.Executable()
	if err != nil {
		slog.Error("Error getting executable path", "err", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	exPath := filepath.Dir(ex)

	// Read server version from git_version file in the executable's directory
	versionBytes, err := os.ReadFile(filepath.Join(exPath, "git_version"))
	version := ""
	if err == nil {
		version = strings.TrimSpace(string(versionBytes))
	} else {
		slog.Error("Error reading git_version file", "err", err)
	}

	resp := NewResponse[any, RT](nil, codes.OK.String(), map[string]any{
		"timestamp":      float64(time.Now().UnixNano()) / 1e9,
		"server_version": version,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func FileServerHandler(dataRoot string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		root, err := os.OpenRoot(dataRoot)
		if err != nil {
			http.Error(w, fmt.Sprintf("server error when opening %s: %v", dataRoot, err), http.StatusInternalServerError)
			return
		}
		// Serve the file
		http.ServeFileFS(w, r, root.FS(), r.URL.Path)
	}
}

func SPAFileServerHandler(dataRoot, routePrefix, fallback string) func(w http.ResponseWriter, r *http.Request) {
	routePrefix = "/" + strings.Trim(routePrefix, "/")
	fallback = strings.TrimPrefix(fallback, "/")
	if fallback == "" {
		fallback = "index.html"
	}

	return func(w http.ResponseWriter, r *http.Request) {
		root, err := os.OpenRoot(dataRoot)
		if err != nil {
			http.Error(w, fmt.Sprintf("server error when opening %s: %v", dataRoot, err), http.StatusInternalServerError)
			return
		}
		defer func() { _ = root.Close() }()

		requestPath := r.URL.Path
		if requestPath == routePrefix {
			http.Redirect(w, r, routePrefix+"/", http.StatusMovedPermanently)
			return
		}
		if !strings.HasPrefix(requestPath, routePrefix+"/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(requestPath, routePrefix+"/")
		if name == "" {
			serveFileFromRoot(w, r, root, fallback)
			return
		}

		cleanName := strings.TrimPrefix(path.Clean("/"+name), "/")
		if serveFileFromRoot(w, r, root, cleanName) {
			return
		}
		if path.Ext(cleanName) != "" || strings.HasPrefix(cleanName, "assets/") {
			http.NotFound(w, r)
			return
		}
		serveFileFromRoot(w, r, root, fallback)
	}
}

func serveFileFromRoot(w http.ResponseWriter, r *http.Request, root *os.Root, name string) bool {
	file, err := root.Open(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true
	}
	if info.IsDir() {
		return false
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
	return true
}

func ProxyHandler(upstream string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse the target URL
		target, err := url.Parse(upstream)
		if err != nil {
			http.Error(w, "Failed to parse target URL", http.StatusInternalServerError)
			return
		}

		// Create the reverse proxy
		proxy := httputil.NewSingleHostReverseProxy(target)

		// Modify the request to forward to the target
		proxy.ServeHTTP(w, r)
	}
}
