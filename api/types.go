package api

type ListResponse[T any] struct {
	Count    int `json:"count"`
	Current  int `json:"current"`
	Next     int `json:"next"`
	Previous int `json:"previous"`
	Last     int `json:"last"`
	Results  []T `json:"results"`
}

// CursorListResponse decodes the Elasticsearch cursor-pagination contract (base64 next token, not ListResponse's int page).
type CursorListResponse[T any] struct {
	Next    string `json:"next"`
	Results []T    `json:"results"`
}
