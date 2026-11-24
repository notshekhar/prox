package systemone

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/localmodels"
	log "github.com/sirupsen/logrus"
)

// Engine answers decision requests; localmodels.Manager is the real one.
type Engine interface {
	Resolve(name string) (localmodels.Model, error)
	Decide(ctx context.Context, m localmodels.Model, req localmodels.Request) (localmodels.Result, error)
	Statuses() []localmodels.Status
}

// Handler serves the System One routes.
type Handler struct{ engine Engine }

func NewHandler(engine Engine) *Handler { return &Handler{engine: engine} }

// Decide is POST /v1/systemone.
func (h *Handler) Decide(c *gin.Context) {
	name, req, ok := buildRequest(c)
	if !ok {
		return
	}
	model, err := h.engine.Resolve(name)
	if err != nil {
		detail(c, http.StatusBadRequest, err.Error())
		return
	}
	result, err := h.engine.Decide(c.Request.Context(), model, req)
	if err != nil {
		h.decideFailed(c, model, err)
		return
	}
	answers, err := shapeAnswers(req.Questions, result.Answers)
	if err != nil {
		detail(c, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(c, http.StatusOK, Response{Model: model.ID, Answers: answers, Usage: Usage{InputTokens: result.InputTokens}})
}

// buildRequest reads, validates and assembles a request and names its model,
// answering the caller itself when it cannot.
func buildRequest(c *gin.Context) (string, localmodels.Request, bool) {
	b, serr := readBody(c.Writer, c.Request)
	if serr != nil {
		detail(c, serr.status, serr.detail)
		return "", localmodels.Request{}, false
	}
	req, fieldErrs := parseRequest(b.json)
	if len(fieldErrs) > 0 {
		writeJSON(c, http.StatusUnprocessableEntity, map[string]any{"detail": fieldErrs})
		return "", localmodels.Request{}, false
	}
	assets, serr := parseAssets(req.Assets, b.files)
	if serr != nil {
		detail(c, serr.status, serr.detail)
		return "", localmodels.Request{}, false
	}
	out := localmodels.Request{State: req.State, Questions: req.Questions, Assets: assets}
	if msg := unsupported(out); msg != "" {
		detail(c, http.StatusBadRequest, msg)
		return "", localmodels.Request{}, false
	}
	return req.Model, out, true
}

func (h *Handler) decideFailed(c *gin.Context, model localmodels.Model, err error) {
	var reqErr *localmodels.RequestError
	if errors.As(err, &reqErr) {
		detail(c, http.StatusBadRequest, reqErr.Message)
		return
	}
	if errors.Is(err, context.Canceled) {
		return // the caller left; nobody reads a reply
	}
	log.Warnf("local model %s: %v", model.ID, err)
	detail(c, http.StatusServiceUnavailable, err.Error())
}

// unsupported names what a valid System One request asks for that local
// models cannot do, or an asset nothing points at.
func unsupported(req localmodels.Request) string {
	for _, q := range req.Questions {
		if q.Type == typeBox {
			return "box questions are not supported by local models yet"
		}
	}
	if id := unreferenced(req); id != "" {
		return fmt.Sprintf("asset %q is not referenced in state, instructions or criteria", id)
	}
	return ""
}

// Models is GET /v1/systemone/models, in System One's model-list shape plus
// what a local model needs: who makes it, whether it is installed, what it
// reads and, for closed-label models, the labels it can answer with.
func (h *Handler) Models(c *gin.Context) {
	type entry struct {
		Name        string                             `json:"name"`
		Description string                             `json:"description"`
		ReleaseDate string                             `json:"release_date"`
		Provider    string                             `json:"provider"`
		Installed   bool                               `json:"installed"`
		Inputs      []string                           `json:"inputs"`
		Labels      map[localmodels.MediaKind][]string `json:"labels,omitempty"`
	}
	models := []entry{}
	for _, st := range h.engine.Statuses() {
		if st.Model.Kind != localmodels.KindDecision {
			continue
		}
		m := st.Model
		inputs := []string{"text"}
		for _, kind := range m.Inputs {
			inputs = append(inputs, string(kind))
		}
		models = append(models, entry{Name: m.ID, Description: m.Description, ReleaseDate: m.ReleaseDate, Provider: m.ProviderName,
			Installed: st.Installed, Inputs: inputs, Labels: st.Labels})
	}
	writeJSON(c, http.StatusOK, map[string]any{"models": models})
}

func detail(c *gin.Context, status int, msg string) {
	writeJSON(c, status, map[string]any{"detail": msg})
}

// writeJSON uses json/v2 so orderedObject keeps the caller's order.
func writeJSON(c *gin.Context, status int, v any) {
	out, err := json.Marshal(v)
	if err != nil {
		c.Data(http.StatusInternalServerError, "application/json", []byte(`{"detail":"could not encode the response"}`))
		return
	}
	c.Data(status, "application/json", out)
}
