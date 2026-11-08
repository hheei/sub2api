package middleware

import (
	"strings"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/masking"
)

// Masking 是全局脱敏接缝：请求体在进入 handler 之前把敏感值换成占位符，
// 响应在写回客户端时还原。
//
// 为什么是中间件：这份代码只需要写一遍就能覆盖 anthropic / chat / responses /
// gemini / systemone 全部协议，而 handler 层的 checkSecurityAudit 之所以有 20 个
// 调用点，是因为它是"拦截/放行"语义，必须知道每个端点的错误格式；脱敏是纯
// 字节变换，不需要。
//
// 必须挂在 apiKeyAuth 之后（需要分组判定）与协议改写中间件（compositeTarget
// 等）之前：这样占位符先于任何内部改写生成，还原晚于全部协议转换。
func Masking(masker *masking.Masker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if masker == nil || c.Request == nil || c.Request.Body == nil {
			c.Next()
			return
		}
		cfg := masker.Config(c.Request.Context())
		if cfg == nil || !cfg.Enabled || !maskableRequest(c, cfg) {
			c.Next()
			return
		}
		body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil || len(body) == 0 {
			// 读失败交给 handler 自己报错，脱敏不抢这个错误。
			c.Next()
			return
		}
		if cfg.MaxBodyBytes > 0 && int64(len(body)) > cfg.MaxBodyBytes {
			// 解压后的体积可能远大于 ContentLength，这里按真实长度再挡一次。
			c.Next()
			return
		}
		masked, registry, hits := masker.Mask(c.Request.Context(), cfg, body)
		// 无论本轮是否命中都注入说明：让多轮会话的 system 段保持稳定。
		masked, noticeInjected := masking.InjectNotice(masked, masking.ProtocolForPath(c.Request.URL.Path), cfg.NoticeText())
		if registry.Len() == 0 && !noticeInjected {
			c.Next()
			return
		}
		requestmodel.ResetRequestBody(c.Request, masked)
		logger.FromContext(c.Request.Context()).Info("masking.request.rewritten",
			zap.Int("hits", hits),
			zap.Int("placeholders", registry.Len()),
			zap.Bool("notice", noticeInjected),
			zap.Bool("tagger", cfg.TaggingEnabled()),
			zap.String("path", c.FullPath()),
		)
		if registry.Len() == 0 {
			c.Next()
			return
		}

		writer := masking.NewRestoreWriter(c.Writer, registry)
		c.Writer = writer
		c.Writer.Header().Del("Content-Length")
		defer writer.Finish()

		logger.FromContext(c.Request.Context()).Info("masking.request.masked",
			zap.Int("hits", hits),
			zap.Int("placeholders", registry.Len()),
			zap.Bool("notice", noticeInjected),
			zap.Bool("tagger", cfg.TaggingEnabled()),
			zap.String("path", c.FullPath()),
		)
		c.Next()
	}
}

// maskableRequest 只处理"JSON 请求体"这一种形态：multipart/二进制、
// 没有 body 的 GET、以及 WebSocket 升级请求一律不碰。
func maskableRequest(c *gin.Context, cfg *masking.Config) bool {
	if c.Request.Method == "" || c.Request.Method == "GET" || c.Request.Method == "HEAD" {
		return false
	}
	if strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
		return false
	}
	contentType := strings.ToLower(c.GetHeader("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "json") {
		return false
	}
	if cfg.MaxBodyBytes > 0 && c.Request.ContentLength > cfg.MaxBodyBytes {
		return false
	}
	return true
}
