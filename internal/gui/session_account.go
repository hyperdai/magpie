package gui

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/yetone/magpie/internal/gateway"
)

// sessionAccountRoutes 将最近请求的真实会话身份用于账号选择，不猜测父会话。
func sessionAccountRoutes(mux *http.ServeMux) {
	route := func(r *http.Request, id int64) (gateway.Route, bool) {
		var rows []gateway.Route
		if gw := served.Load(); gw != nil {
			rows = gw.Trace(r.Context(), 0, 0).Routes
		} else if st, ok := gateway.OwnerTrace(r.Context(), 0, 0); ok {
			rows = st.Routes
		}
		for _, row := range rows {
			if row.ID == id && row.Agent == "codex" && row.Provider == "codex" && row.Group == nil && row.Session != "" {
				return row, true
			}
		}
		return gateway.Route{}, false
	}
	mux.HandleFunc("GET /api/gateway/session-account", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		row, ok := route(r, id)
		if !ok {
			http.Error(w, "select a recent Codex subscription request with a session id", http.StatusNotFound)
			return
		}
		out, err := gateway.SessionAccount(row.Session)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("POST /api/gateway/session-account", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID      int64  `json:"id"`
			Account string `json:"account"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid account selection", http.StatusBadRequest)
			return
		}
		row, ok := route(r, in.ID)
		if !ok {
			http.Error(w, "select a recent Codex subscription request with a session id", http.StatusNotFound)
			return
		}
		if err := gateway.SetSessionAccount(row.Session, in.Account); err != nil {
			fail(w, err)
			return
		}
		out, err := gateway.SessionAccount(row.Session)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, out)
	})
}
