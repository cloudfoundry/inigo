package api

import "github.com/tedsuo/rata"

const (
	LRPStatus = "LRPStatus"
)

var Routes = rata.Routes{
	{Path: "/lrps/:guid", Method: "GET", Name: LRPStatus},
}
