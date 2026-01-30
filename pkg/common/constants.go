package common

import (
	"os"
	"syscall"
)

// Signals to listen to:
// 1. os.Interrup -> allows devs to easily run a server locally
// 2. syscall.SIGTERM -> sent by kubernetes when stopping a server gracefully
var InterruptSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}
