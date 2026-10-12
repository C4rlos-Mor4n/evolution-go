package whatsmeow_service

import (
	"errors"
	"testing"

	"github.com/evolution-foundation/evolution-go/pkg/config"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
)

type stubProducer struct {
	err   error
	calls int
}

func (p *stubProducer) Produce(string, []byte, string, string) error {
	p.calls++
	return p.err
}

func (p *stubProducer) CreateGlobalQueues() error { return nil }

func TestSendToQueueOrWebhookDeliversWebhookWhenQueuesFail(t *testing.T) {
	failing := errors.New("broker down")
	rabbit, nats, ws, hook := &stubProducer{err: failing}, &stubProducer{err: failing}, &stubProducer{err: failing}, &stubProducer{}
	w := &whatsmeowService{
		rabbitmqProducer:  rabbit,
		natsProducer:      nats,
		websocketProducer: ws,
		webhookProducer:   hook,
		loggerWrapper:     logger_wrapper.NewLoggerManager(&config.Config{LogDirectory: t.TempDir(), LogMaxSize: 1}),
	}
	inst := &instance_model.Instance{
		Id: "inst", RabbitmqEnable: "enabled", NatsEnable: "enabled", WebSocketEnable: "enabled",
		Webhook: "http://example.invalid/hook",
	}

	w.sendToQueueOrWebhook(inst, "inst.message", []byte(`{}`))

	for name, p := range map[string]*stubProducer{"rabbitmq": rabbit, "nats": nats, "websocket": ws, "webhook": hook} {
		if p.calls != 1 {
			t.Errorf("%s producer called %d times, want 1", name, p.calls)
		}
	}
}
