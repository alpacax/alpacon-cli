package api

type ListResponse[T any] struct {
	Count    int    `json:"count"`
	Current  int    `json:"current"`
	Next     int    `json:"next"`
	Previous string `json:"previous"`
	Last     int    `json:"last"`
	Results  []T    `json:"results"`
}

type Owner struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type RequestedBy struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}
