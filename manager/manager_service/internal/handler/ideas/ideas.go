package ideas

import (
	"errors"
	"net/http"

	v1 "github.com/KyrieWang7/nous-agent/manager/manager_service/api/v1"
	model "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/model/ideas"
	"github.com/KyrieWang7/nous-agent/manager/manager_service/internal/service/ideas"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Handler struct {
	service *ideas.Service
	logger  *zap.Logger
}

func New(service *ideas.Service, logger *zap.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

func (h *Handler) List(c *gin.Context) {
	items, err := h.service.List(c.Request.Context())
	if h.writeError(c, err) {
		return
	}
	result := make([]v1.Idea, 0, len(items))
	for _, item := range items {
		result = append(result, response(item))
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) Create(c *gin.Context) {
	draft, ok := bind(c)
	if !ok {
		return
	}
	item, err := h.service.Create(c.Request.Context(), draft)
	if !h.writeError(c, err) {
		c.JSON(http.StatusOK, response(item))
	}
}

func (h *Handler) Update(c *gin.Context) {
	draft, ok := bind(c)
	if !ok {
		return
	}
	item, err := h.service.Update(c.Request.Context(), c.Param("iid"), draft)
	if !h.writeError(c, err) {
		c.JSON(http.StatusOK, response(item))
	}
}

func (h *Handler) Delete(c *gin.Context) {
	id := c.Param("iid")
	if !h.writeError(c, h.service.Delete(c.Request.Context(), id)) {
		c.JSON(http.StatusOK, v1.DeleteIdea{OK: true, Deleted: id})
	}
}

func bind(c *gin.Context) (ideas.Draft, bool) {
	var input v1.IdeaInput
	if err := c.ShouldBindJSON(&input); err != nil {
		validation(c, []string{"body"}, "Invalid request body", "value_error")
		return ideas.Draft{}, false
	}
	if input.Title == nil {
		validation(c, []string{"body", "title"}, "Field required", "missing")
		return ideas.Draft{}, false
	}
	status := "pending"
	if input.Status != nil {
		status = *input.Status
	}
	return ideas.Draft{Title: *input.Title, Note: input.Note, Source: input.Source, Status: status}, true
}

func validation(c *gin.Context, field []string, message, kind string) {
	c.JSON(http.StatusUnprocessableEntity, struct {
		Detail []v1.ValidationIssue `json:"detail"`
	}{Detail: []v1.ValidationIssue{{Field: field, Message: message, Type: kind}}})
}

func response(item model.Idea) v1.Idea {
	return v1.Idea{ID: item.ID, Title: item.Title, Note: item.Note,
		Source: item.Source, Status: item.Status, Created: item.CreatedAt.Unix()}
}

func (h *Handler) writeError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ideas.ErrNotFound) {
		c.JSON(http.StatusNotFound, v1.Error{Detail: err.Error()})
		return true
	}
	h.logger.Error("ideas operation failed", zap.String("route", c.FullPath()))
	c.JSON(http.StatusInternalServerError, v1.Error{Detail: "选题操作失败"})
	return true
}
