package repository

import (
	"database/sql"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmeta"
)

func ingressJSON(value *requestmeta.Ingress) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return string(data)
}

func parseIngressJSON(value sql.NullString) *requestmeta.Ingress {
	if !value.Valid || value.String == "null" {
		return nil
	}
	var ingress requestmeta.Ingress
	if json.Unmarshal([]byte(value.String), &ingress) != nil {
		return nil
	}
	return &ingress
}
