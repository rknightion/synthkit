// SPDX-License-Identifier: AGPL-3.0-only

package irm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestResetResolvesOpenIncidentsAndAlertGroups: the OnCall host comes from the IRM app
// settings, and open incidents and alert groups are resolved across pages (resolved ones are
// left), each request carrying the token and the stack URL.
func TestResetResolvesOpenIncidentsAndAlertGroups(t *testing.T) {
	var calls []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("X-Grafana-URL") != srv.URL {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(body)))
		switch {
		case r.URL.Path == "/api/plugins/grafana-irm-app/settings":
			_, _ = io.WriteString(w, `{"jsonData":{"onCallApiUrl":"`+srv.URL+`/oncall"}}`)
		case strings.HasSuffix(r.URL.Path, "QueryIncidents"):
			_, _ = io.WriteString(w, `{"incidents":[{"incidentID":"1","status":"resolved","title":"old"},{"incidentID":"2","status":"active","title":"live"}],"cursor":{"hasMore":false}}`)
		case r.URL.Path == "/oncall/api/v1/alert_groups/" && r.URL.Query().Get("page") == "":
			_, _ = io.WriteString(w, `{"next":"`+srv.URL+`/oncall/api/v1/alert_groups/?page=2","results":[{"id":"A","state":"firing"},{"id":"Z","state":"resolved"}]}`)
		case r.URL.Path == "/oncall/api/v1/alert_groups/":
			_, _ = io.WriteString(w, `{"next":null,"results":[{"id":"B","state":"acknowledged"}]}`)
		case strings.HasSuffix(r.URL.Path, "/resolve/"):
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()
	rep, err := Reset(context.Background(), Config{GrafanaURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /api/plugins/grafana-irm-app/settings",
		`POST /api/plugins/grafana-irm-app/resources/api/v1/IncidentsService.QueryIncidents {"query":{"limit":100,"orderDirection":"DESC"}}`,
		`POST /api/plugins/grafana-irm-app/resources/api/v1/IncidentsService.UpdateStatus {"incidentID":"2","status":"resolved"}`,
		"GET /oncall/api/v1/alert_groups/?perpage=100",
		"GET /oncall/api/v1/alert_groups/?page=2",
		"POST /oncall/api/v1/alert_groups/A/resolve/",
		"POST /oncall/api/v1/alert_groups/B/resolve/",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	if rep.AlertGroupsResolved != 2 || rep.IncidentsResolved != 1 || len(rep.Problems) != 0 {
		t.Fatalf("report %+v", rep)
	}
}
