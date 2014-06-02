package api

import "github.com/tedsuo/router"

const (
	LRPStatus = "LRPStatus"
)

var Routes = router.Routes{
	{Path: "/lrps/:guid", Method: "GET", Handler: LRPStatus},
}
