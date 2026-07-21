package monitor

import (
	"net/http"
	"strings"

	"easy_proxies/internal/accesslog"
)

// handleAccessLogsList GET /api/v1/access-logs：访问日志（时间倒序，支持 ip/result 过滤 + 分页）。
// 数据来自内存环形缓冲（accesslog.Default），重启后清空。未启用访问日志时返回空列表。
func (s *Server) handleAccessLogsList(w http.ResponseWriter, r *http.Request) {
	page, pageSize, offset := parsePage(r)
	l := accesslog.Default()
	if l == nil {
		writeJSON(w, newPageResponse([]accesslog.Entry{}, 0, page, pageSize))
		return
	}
	q := r.URL.Query()
	filter := accesslog.Filter{
		SrcIP:   strings.TrimSpace(q.Get("ip")),
		Verdict: strings.TrimSpace(q.Get("result")), // allow / deny
	}
	all := l.Recent(filter, 0) // 倒序全部匹配（管理查询低频，可接受全量拷贝后再分页）
	total := len(all)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	writeJSON(w, newPageResponse(all[offset:end], total, page, pageSize))
}
