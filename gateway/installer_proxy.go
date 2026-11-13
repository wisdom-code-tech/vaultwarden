package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func installerProxy(client *http.Client, route string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			origin, err := url.Parse(request.Header.Get("Origin"))
			if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.Host != request.Host {
				writeManagementJSON(writer, http.StatusForbidden, map[string]string{"error": "安装请求必须来自当前管理页面"})
				return
			}
			if strings.Split(request.Header.Get("Content-Type"), ";")[0] != "application/json" {
				writeManagementJSON(writer, http.StatusUnsupportedMediaType, map[string]string{"error": "安装请求必须为 JSON"})
				return
			}
			decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024))
			decoder.DisallowUnknownFields()
			var body struct{}
			if err := decoder.Decode(&body); err != nil {
				writeManagementJSON(writer, http.StatusBadRequest, map[string]string{"error": "安装请求不接受 URL、文件路径或版本参数"})
				return
			}
			if err := decoder.Decode(&body); err != io.EOF {
				writeManagementJSON(writer, http.StatusBadRequest, map[string]string{"error": "安装请求只能包含一个空 JSON 对象"})
				return
			}
		}
		forwarded, err := http.NewRequestWithContext(request.Context(), request.Method, "http://localhost/"+route, nil)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		response, err := client.Do(forwarded)
		if err != nil {
			writeManagementJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "升级辅助服务未启用或暂不可用，请管理员先在 NAS 上安装辅助服务；仍可手动下载 FPK"})
			return
		}
		defer response.Body.Close()
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.WriteHeader(response.StatusCode)
		io.Copy(writer, io.LimitReader(response.Body, 65536))
	}
}

func registerInstallerRoutes(mux *http.ServeMux) {
	client := unixHTTPClient(updaterSocket, 6*time.Second)
	mux.HandleFunc("GET /api/installer", installerProxy(client, "capability"))
	mux.HandleFunc("GET /api/install-status", installerProxy(client, "status"))
	mux.HandleFunc("POST /api/install", installerProxy(client, "install"))
}
