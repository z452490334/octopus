package relay

import (
	"os"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/conf"
	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/gin-gonic/gin"
)

// maxSSEEventSize 定义普通 relay SSE 单事件的最大大小。
// 普通文本流不应接受几十 MB 的单事件，否则读取、解析、转换时会形成多份大对象。
var maxSSEEventSize = 4 * 1024 * 1024

// maxImagesSSEEventSize 定义 images relay SSE 单事件的最大大小。
// 图片流可能携带较大的 base64 数据，因此保留单独较高上限。
var maxImagesSSEEventSize = 32 * 1024 * 1024

func init() {
	maxSSEEventSize = envPositiveInt(strings.ToUpper(conf.APP_NAME)+"_RELAY_MAX_SSE_EVENT_SIZE", maxSSEEventSize)
	maxImagesSSEEventSize = envPositiveInt(strings.ToUpper(conf.APP_NAME)+"_IMAGES_MAX_SSE_EVENT_SIZE", maxImagesSSEEventSize)
}

func envPositiveInt(name string, def int) int {
	if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return def
}

// hopByHopHeaders 定义不应转发的 HTTP 头
var hopByHopHeaders = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
	"content-length":      true,
	"host":                true,
	"accept-encoding":     true,
	"x-forwarded-for":     true,
	"x-forwarded-host":    true,
	"x-forwarded-proto":   true,
	"x-forwarded-port":    true,
	"x-real-ip":           true,
	"forwarded":           true,
	"cf-connecting-ip":    true,
	"true-client-ip":      true,
	"x-client-ip":         true,
	"x-cluster-client-ip": true,
}

type relayRequest struct {
	c               *gin.Context
	inboundType     inbound.InboundType
	inAdapter       model.Inbound
	internalRequest *model.InternalLLMRequest
	metrics         *RelayMetrics
	apiKeyID        int
	requestModel    string
	iter            *balancer.Iterator
}

// relayAttempt 尝试级上下文
type relayAttempt struct {
	*relayRequest // 嵌入请求级上下文

	outAdapter           model.Outbound
	channel              *dbmodel.Channel
	usedKey              dbmodel.ChannelKey
	firstTokenTimeOutSec int
}

// attemptResult 封装单次尝试的结果
type attemptResult struct {
	Success bool  // 是否成功
	Written bool  // 流式响应是否已开始写入（不可重试）
	Err     error // 失败时的错误
}
