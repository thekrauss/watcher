package restate

import (
	"net/http"
)

type RestateClient struct {
	URL string
}

func NewRestateClient(url string) *RestateClient {
	return &RestateClient{URL: url}
}
func (c *RestateClient) NotifyEvent(event interface{}) {
	http.Post(c.URL+"/ProjectService/123/onPodEvent", "application/json", nil)
}
