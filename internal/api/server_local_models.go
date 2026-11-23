package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/localmodels"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/systemone"
)

// registerSystemOneRoutes serves the System One decision API from local
// models. They need no sign-in, so the route only checks prox's client key.
func (s *Server) registerSystemOneRoutes(v1 *gin.RouterGroup) {
	h := systemone.NewHandler(s.localModels)
	v1.POST("/systemone", h.Decide)
	v1.GET("/systemone/models", h.Models)
}

// registerLocalModelManagementRoutes lets the dashboard list, install and remove local models.
func (s *Server) registerLocalModelManagementRoutes(v8 *gin.RouterGroup) {
	v8.GET("/local-models", s.listLocalModels)
	v8.POST("/local-models/:id/install", s.installLocalModel)
	v8.DELETE("/local-models/:id", s.removeLocalModel)
}

type localModelView struct {
	ID           string                       `json:"id"`
	Provider     string                       `json:"provider"`
	ProviderName string                       `json:"provider_name"`
	Description  string                       `json:"description"`
	Kind         string                       `json:"kind"`
	Repo         string                       `json:"repo"`
	DownloadSize string                       `json:"download_size"`
	Installed    bool                         `json:"installed"`
	Running      bool                         `json:"running"`
	Device       string                       `json:"device,omitempty"`
	Install      *localmodels.InstallProgress `json:"install,omitempty"`
}

func (s *Server) listLocalModels(c *gin.Context) {
	statuses := s.localModels.Statuses()
	views := make([]localModelView, 0, len(statuses))
	for _, st := range statuses {
		m := st.Model
		views = append(views, localModelView{
			ID: m.ID, Provider: m.Provider, ProviderName: m.ProviderName, Description: m.Description,
			Kind: string(m.Kind), Repo: m.Repo, DownloadSize: m.DownloadSize,
			Installed: st.Installed, Running: st.Running, Device: st.Device, Install: st.Install,
		})
	}
	c.JSON(http.StatusOK, gin.H{"models": views})
}

func (s *Server) installLocalModel(c *gin.Context) {
	if err := s.localModels.StartInstall(c.Param("id")); err != nil {
		localModelError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "installing"})
}

func (s *Server) removeLocalModel(c *gin.Context) {
	if err := s.localModels.Remove(c.Request.Context(), c.Param("id")); err != nil {
		localModelError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "removed"})
}

func localModelError(c *gin.Context, err error) {
	status := http.StatusConflict
	if errors.Is(err, localmodels.ErrUnknownModel) {
		status = http.StatusNotFound
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
