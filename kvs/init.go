package kvs

import logxi "github.com/processout/logxi/lib"

var logger logxi.Logger

func init() {
	logger = logxi.New("dat.cache")
}
