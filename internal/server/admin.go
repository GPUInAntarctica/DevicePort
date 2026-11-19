package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// AdminHandler returns an HTTP handler for server administration. It is meant
// to be bound to localhost (or a management network) by the caller.
func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/networks", s.adminNetworks)
	mux.HandleFunc("/api/networks/", s.adminNetwork)
	return mux
}

func writeAdminJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) adminNetworks(w http.ResponseWriter, _ *http.Request) {
	writeAdminJSON(w, s.Networks())
}

// /api/networks/{name}[/auth|/require-auth|/routes]
func (s *Server) adminNetwork(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/networks/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	name := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	if action == "" && r.Method == http.MethodGet {
		for _, v := range s.Networks() {
			if v.Name == name {
				writeAdminJSON(w, v)
				return
			}
		}
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "POST or DELETE required", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()

	switch action {
	case "auth":
		id := r.FormValue("id")
		if len(id) != 32 {
			http.Error(w, "id must be a 32-character node ID", http.StatusBadRequest)
			return
		}
		allow := strings.EqualFold(r.FormValue("allow"), "on") || strings.EqualFold(r.FormValue("allow"), "true") || r.FormValue("allow") == "1"
		if err := s.Authorize(name, id, allow); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeAdminJSON(w, map[string]string{"ok": "true"})
	case "require-auth":
		require := strings.EqualFold(r.FormValue("value"), "on") || strings.EqualFold(r.FormValue("value"), "true") || r.FormValue("value") == "1"
		s.SetNetworkAuth(name, require)
		writeAdminJSON(w, map[string]string{"ok": "true"})
	case "routes":
		cidr := r.FormValue("cidr")
		if cidr == "" {
			http.Error(w, "cidr required", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodDelete {
			if err := s.RemoveRoute(name, cidr); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			via := r.FormValue("via")
			if len(via) != 32 {
				http.Error(w, "via must be a 32-character node ID", http.StatusBadRequest)
				return
			}
			if err := s.AddRoute(name, cidr, via); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		writeAdminJSON(w, map[string]string{"ok": "true"})
	default:
		http.NotFound(w, r)
	}
}
