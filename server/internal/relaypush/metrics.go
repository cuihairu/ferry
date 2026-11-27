package relaypush

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Config delivery outcome counter (F-1808): both delivery funnels report here
// — relaypush.Push (manual and automatic config pushes / relay re-pointings)
// and the handler's rule-lib push. Counted at event time; the success rate is
// applied / total at query time.
var PushOutcome = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "ferry_config_push_total",
	Help: "Config push outcomes by result (applied/invalid/bad_ack/offline/timeout/error/db_error).",
}, []string{"result"})
