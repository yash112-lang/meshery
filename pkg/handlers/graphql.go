package handlers

import (
	"net/http"

	"github.com/layer5io/meshery/pkg/models"
)

func (h *Handler) GraphqlSystemHandler(w http.ResponseWriter, req *http.Request, prefObj *models.Preference, user *models.User, provider models.Provider) {
	queryEndpoint := "/api/system/graphql/query"
	playgroundEndpoint := "/api/system/graphql/playground"

	if req.URL.Path == queryEndpoint {
		h.gqlHandler.ServeHTTP(w, req)
	} else if req.URL.Path == playgroundEndpoint {
		h.gqlPlayground.ServeHTTP(w, req)
	} else {
		http.Error(w, "Invalid endpoint", http.StatusInternalServerError)
	}
}
