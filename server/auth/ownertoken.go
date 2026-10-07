package auth

import (
	"net/http"

	"github.com/WWestC/Niuma_Studio/server/httputil"
)

func (au *Face) HandleOwnerToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !isLoopbackAddr(r.RemoteAddr) {
		http.NotFound(w, r)
		return
	}
	if au.Registry == nil {
		http.NotFound(w, r)
		return
	}
	tok := au.Registry.OwnerCredential()
	if tok == "" {
		http.NotFound(w, r)
		return
	}
	httputil.WriteJSON(w, map[string]any{"token": tok, "name": au.Local})
}

func isLoopbackAddr(remote string) bool { return httputil.LoopbackAddr(remote) }
