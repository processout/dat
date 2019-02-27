package kvs

import "github.com/processout/logxi"

var logger logxi.Logger

func init() {
	logger = logxi.New("dat.cache")
}
