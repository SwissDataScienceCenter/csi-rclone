package rclone

import (
	"github.com/prometheus/client_golang/prometheus"
)

type Sizeable interface {
	Size() int
}

type SizeableMeter struct {
	meter prometheus.Gauge
	sized Sizeable
}
