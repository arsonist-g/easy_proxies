package monitor

import (
	"net/http"
	"strconv"
)

const (
	defaultPage     = 1
	defaultPageSize = 20
	maxPageSize     = 100
)

// parsePage 解析 ?page= &page_size=（page 从 1，page_size 默认 20 上限 100），返回 page/pageSize/offset。
func parsePage(r *http.Request) (page, pageSize, offset int) {
	page = defaultPage
	pageSize = defaultPageSize
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := r.URL.Query().Get("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pageSize = n
		}
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	offset = (page - 1) * pageSize
	return page, pageSize, offset
}

// pageResponse 分页响应（api-contract §2.2）。
type pageResponse struct {
	Items    interface{} `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

func newPageResponse(items interface{}, total, page, pageSize int) pageResponse {
	return pageResponse{Items: items, Total: total, Page: page, PageSize: pageSize}
}
