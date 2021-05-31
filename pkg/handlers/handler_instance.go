//Package handlers :  collection of handlers (aka "HTTP middleware")
package handlers

import (
	"net/http"

	"github.com/layer5io/meshery/internal/channels"
	"github.com/layer5io/meshery/pkg/graphql"
	"github.com/layer5io/meshery/pkg/models"
	"github.com/layer5io/meshkit/logger"
	mesherykube "github.com/layer5io/meshkit/utils/kubernetes"
	"github.com/vmihailenco/taskq/v3"
)

// Handler type is the bucket for configs and http handlers
type Handler struct {
	config        *models.HandlerConfig
	task          *taskq.Task
	kubeclient    *mesherykube.Client
	log           logger.Handler
	gqlHandler    http.Handler
	gqlPlayground http.Handler
}

// NewHandlerInstance returns a Handler instance
func NewHandlerInstance(
	handlerConfig *models.HandlerConfig,
	client *mesherykube.Client,
	logger logger.Handler,
) models.HandlerInterface {
	handlerConfig.Channels = map[string]channels.GenericChannel{
		channels.MeshSync:        channels.NewMeshSyncChannel(),
		channels.BrokerPublish:   channels.NewBrokerPublishChannel(),
		channels.BrokerSubscribe: channels.NewBrokerSubscribeChannel(),
	}

	h := &Handler{
		config:     handlerConfig,
		kubeclient: client,
		log:        logger,
		gqlHandler: graphql.New(graphql.Options{
			Logger:        logger,
			DBHandler:     handlerConfig.Providers["None"].GetGenericPersister(),
			KubeClient:    client,
			HandlerConfig: handlerConfig,
		}),
		gqlPlayground: graphql.NewPlayground(graphql.Options{
			URL: "/api/system/graphql/query",
		}),
	}

	h.task = taskq.RegisterTask(&taskq.TaskOptions{
		Name:    "submitMetrics",
		Handler: h.CollectStaticMetrics,
	})

	return h
}
