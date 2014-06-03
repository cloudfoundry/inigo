package api

type LRPInstance struct {
	ProcessGuid  string `json:"process_guid"`
	InstanceGuid string `json:"instance_guid"`
	Index        uint   `json:"index"`
	State        string `json:"state"`
	Since        int64  `json:"since_in_ns"`
}
