package auth_middleware

import (
	"net/http"

	"github.com/evolution-foundation/evolution-go/pkg/config"
	instance_service "github.com/evolution-foundation/evolution-go/pkg/instance/service"
	"github.com/gin-gonic/gin"
)

type Middleware interface {
	Auth(ctx *gin.Context)
	AuthAdmin(ctx *gin.Context)
	AuthHeaderOrQuery(ctx *gin.Context)
}

type middleware struct {
	config          *config.Config
	instanceService instance_service.InstanceService
}

func (m middleware) Auth(ctx *gin.Context) {
	token := ctx.GetHeader("apikey")
	if token == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authorized"})
		return
	}

	instance, err := m.instanceService.GetInstanceByToken(token)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authorized"})
		return
	}

	ctx.Set("instance", instance)

	ctx.Next()
}

// AuthHeaderOrQuery is the instance auth for WebSocket endpoints. It is the
// same check as Auth (instance token -> instance), but because browser
// WebSocket clients cannot set custom headers on the upgrade request it also
// accepts the token as the "apikey" query parameter. The header wins when both
// are present. Prefer the header: query strings can end up in access logs.
func (m middleware) AuthHeaderOrQuery(ctx *gin.Context) {
	token := ctx.GetHeader("apikey")
	if token == "" {
		token = ctx.Query("apikey")
	}
	if token == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authorized"})
		return
	}

	instance, err := m.instanceService.GetInstanceByToken(token)
	if err != nil || instance == nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authorized"})
		return
	}

	ctx.Set("instance", instance)

	ctx.Next()
}

func (m middleware) AuthAdmin(ctx *gin.Context) {
	token := ctx.GetHeader("apikey")
	if token == "" {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authorized"})
		return
	}

	if token != m.config.GlobalApiKey {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authorized"})
		return
	}

	ctx.Next()
}

func NewMiddleware(config *config.Config, instanceService instance_service.InstanceService) *middleware {
	return &middleware{config: config, instanceService: instanceService}
}
