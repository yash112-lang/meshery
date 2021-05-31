package channels

import "github.com/layer5io/meshkit/broker"

var (
	MeshSync        = "meshsync"
	BrokerPublish   = "broker-publish"
	BrokerSubscribe = "broker-subscribe"
)

type GenericChannel interface {
	Stop()
}

func NewMeshSyncChannel() MeshSyncChannel {
	return make(chan struct{})
}

type MeshSyncChannel chan struct{}

func (ch MeshSyncChannel) Stop() {
	<-ch
}

func NewBrokerSubscribeChannel() BrokerSubscribeChannel {
	return make(chan *broker.Message)
}

type BrokerSubscribeChannel chan *broker.Message

func (ch BrokerSubscribeChannel) Stop() {
	<-ch
}

func NewBrokerPublishChannel() BrokerPublishChannel {
	return make(chan *broker.Message)
}

type BrokerPublishChannel chan *broker.Message

func (ch BrokerPublishChannel) Stop() {
	<-ch
}
