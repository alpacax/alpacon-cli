package api

type ListResponse[T any] struct {
	Count    int `json:"count"`
	Current  int `json:"current"`
	Next     int `json:"next"`
	Previous int `json:"previous"`
	Last     int `json:"last"`
	Results  []T `json:"results"`
}
