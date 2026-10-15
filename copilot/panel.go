package main

import (
	_ "embed"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

//go:embed panel.html
var panelHTML []byte

// handlePanelPage 返回内嵌的管理面板页面（已包装为标准 envelope）。
func handlePanelPage() []byte {
	resp := pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers: http.Header{
			"Content-Type":  []string{"text/html; charset=utf-8"},
			"Cache-Control": []string{"no-cache, no-store, must-revalidate"},
		},
		Body: panelHTML,
	}
	data, _ := okEnvelope(resp)
	return data
}
