package systemone

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/localmodels"
	log "github.com/sirupsen/logrus"
)

// maxBody bounds a request; System One's own limit is 64k tokens of state.
const maxBody = 32 << 20

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
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBody))
	if err != nil {
		detail(c, http.StatusBadRequest, "could not read the request body")
		return
	}
	req, fieldErrs := parseRequest(body)
	if len(fieldErrs) > 0 {
		writeJSON(c, http.StatusUnprocessableEntity, map[string]any{"detail": fieldErrs})
		return
	}
	if msg := unsupported(req); msg != "" {
		detail(c, http.StatusBadRequest, msg)
		return
	}
	model, err := h.engine.Resolve(req.Model)
	if err != nil {
		detail(c, http.StatusBadRequest, err.Error())
		return
	}
	h.answer(c, model, req)
}

func (h *Handler) answer(c *gin.Context, model localmodels.Model, req parsed) {
	result, err := h.engine.Decide(c.Request.Context(), model, localmodels.Request{State: req.State, Questions: questionsJSON(req.Questions)})
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

// unsupported names what a valid System One request asks for that local models cannot do yet.
func unsupported(req parsed) string {
	if req.HasAssets {
		return "image assets are not supported by local models yet; send a text or JSON state"
	}
	for _, q := range req.Questions {
		if q.Type == typeBox {
			return "box questions need images, which local models do not support yet"
		}
	}
	return ""
}

// questionsJSON rebuilds the questions object in the caller's order.
func questionsJSON(questions []question) []byte {
	obj := make(orderedObject, len(questions))
	for i, q := range questions {
		obj[i] = member{q.Name, q}
	}
	out, _ := obj.MarshalJSON()
	return out
}

// Models is GET /v1/systemone/models, in System One's model-list shape plus
// what a local model needs: who makes it and whether it is installed.
func (h *Handler) Models(c *gin.Context) {
	type entry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		ReleaseDate string `json:"release_date"`
		Provider    string `json:"provider"`
		Installed   bool   `json:"installed"`
	}
	models := []entry{}
	for _, st := range h.engine.Statuses() {
		if st.Model.Kind != localmodels.KindDecision {
			continue
		}
		m := st.Model
		models = append(models, entry{Name: m.ID, Description: m.Description, ReleaseDate: m.ReleaseDate, Provider: m.ProviderName, Installed: st.Installed})
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
