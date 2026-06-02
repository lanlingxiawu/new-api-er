// xiugai 添加号池节点功能
package controller

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

var (
	nodePoolClient     *http.Client
	nodePoolClientOnce sync.Once
)

func getNodePoolClient() *http.Client {
	nodePoolClientOnce.Do(func() {
		nodePoolClient = buildNodePoolClient()
	})
	return nodePoolClient
}

func buildNodePoolClient() *http.Client {
	certFile := os.Getenv("NODE_POOL_CLIENT_CERT")
	keyFile := os.Getenv("NODE_POOL_CLIENT_KEY")
	caFile := os.Getenv("NODE_POOL_CA_CERT")

	if certFile == "" || keyFile == "" {
		return &http.Client{Timeout: 15 * time.Second}
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		common.SysError(fmt.Sprintf("node pool: failed to load client cert/key: %v", err))
		return &http.Client{Timeout: 15 * time.Second}
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	if caFile != "" {
		caCert, err := os.ReadFile(caFile)
		if err != nil {
			common.SysError(fmt.Sprintf("node pool: failed to read CA cert: %v", err))
		} else {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caCert) {
				common.SysError("node pool: failed to parse CA cert")
			} else {
				tlsConfig.RootCAs = pool
			}
		}
	}

	transport := &http.Transport{TLSClientConfig: tlsConfig}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}
}

func getNodeControlBaseUrl() string {
	u := system_setting.NodeControlServiceUrl
	return strings.TrimRight(u, "/")
}

func proxyNodePoolRequest(c *gin.Context, targetPath string, method string) {
	baseUrl := getNodeControlBaseUrl()
	if baseUrl == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "节点控制服务地址未配置",
		})
		return
	}

	targetUrl := fmt.Sprintf("%s%s", baseUrl, targetPath)
	req, err := http.NewRequest(method, targetUrl, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := getNodePoolClient().Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": "请求节点控制服务失败: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	c.Status(resp.StatusCode)
	c.Header("Content-Type", "application/json")
	io.Copy(c.Writer, resp.Body)
}

func GetNodePoolNodes(c *gin.Context) {
	proxyNodePoolRequest(c, "/api/v1/nodes", http.MethodGet)
}

func GetNodePoolAccounts(c *gin.Context) {
	publicIP := c.Param("public_ip")
	nodeName := c.Param("node_name")
	proxyNodePoolRequest(c, fmt.Sprintf("/api/v1/nodes/%s/%s/accounts", publicIP, nodeName), http.MethodGet)
}

func DeleteNodePoolNode(c *gin.Context) {
	publicIP := c.Param("public_ip")
	nodeName := c.Param("node_name")
	proxyNodePoolRequest(c, fmt.Sprintf("/api/v1/nodes/%s/%s", publicIP, nodeName), http.MethodDelete)
}

// end
