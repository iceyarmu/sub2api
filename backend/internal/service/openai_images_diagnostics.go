package service

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const openAIImagesRequestLogFieldsKey = "openai_images_request_log_fields"

// OpenAIImagesRequestLogFields returns the last attempt's safe request metadata.
func OpenAIImagesRequestLogFields(c *gin.Context) []zap.Field {
	if c == nil {
		return nil
	}
	value, _ := c.Get(openAIImagesRequestLogFieldsKey)
	fields, _ := value.([]zap.Field)
	return fields
}

// OpenAIImagesFailoverLogFields preserves the error summary hidden by the
// failover sentinel's generic Error(), without logging the full response body.
func OpenAIImagesFailoverLogFields(err *UpstreamFailoverError) []zap.Field {
	if err == nil {
		return nil
	}
	upstreamErr := openAIImagesUpstreamErrorFromHTTP(err.StatusCode, err.ResponseHeaders, err.ResponseBody)
	return []zap.Field{
		zap.String("upstream_error_message", truncateString(logredact.RedactText(upstreamErr.Message, "api_key", "authorization"), 2048)),
		zap.String("error_type", upstreamErr.ErrorType),
		zap.String("error_code", upstreamErr.Code),
		zap.String("error_param", upstreamErr.Param),
		zap.String("upstream_request_id", upstreamErr.UpstreamRequestID),
	}
}

// Store only routing and image options, never the prompt, image data or credentials.
func setOpenAIImagesRequestLogFields(c *gin.Context, clientModel, routingModel, endpoint string, body []byte, direct bool) {
	requestField := func(path string) string {
		value := gjson.GetBytes(body, path)
		if !value.Exists() {
			return "<unset>"
		}
		return value.String()
	}
	imagePrefix := ""
	if !direct {
		imagePrefix = "tools.0."
	}
	c.Set(openAIImagesRequestLogFieldsKey, []zap.Field{
		zap.String("request_model", clientModel),
		zap.String("routing_model", routingModel),
		zap.String("upstream_model", requestField(imagePrefix+"model")),
		zap.String("upstream_request_model", requestField("model")),
		zap.String("upstream_endpoint", endpoint),
		zap.String("reasoning_effort", requestField("reasoning.effort")),
		zap.String("image_quality", requestField(imagePrefix+"quality")),
		zap.String("image_complexity", requestField(imagePrefix+"complexity")),
	})
}

func setOpenAIImagesResponseLogFields(c *gin.Context, resp *http.Response) {
	fields := append(OpenAIImagesRequestLogFields(c),
		zap.Int("upstream_http_status", resp.StatusCode),
		zap.String("upstream_content_type", resp.Header.Get("Content-Type")),
	)
	c.Set(openAIImagesRequestLogFieldsKey, fields)
}
