// xiugai 添加号池节点功能
package controller

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
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
	nodePoolClientErr  error
	nodePoolClientOnce sync.Once
)

func getNodePoolClient() (*http.Client, error) {
	nodePoolClientOnce.Do(func() {
		nodePoolClient, nodePoolClientErr = buildNodePoolClient()
	})
	return nodePoolClient, nodePoolClientErr
}

func buildNodePoolClient() (*http.Client, error) {
	certFile := os.Getenv("NODE_POOL_CLIENT_CERT")
	keyFile := os.Getenv("NODE_POOL_CLIENT_KEY")
	caFile := os.Getenv("NODE_POOL_CA_CERT")

	if certFile == "" || keyFile == "" || caFile == "" {
		return nil, errors.New("node pool: NODE_POOL_CLIENT_CERT、NODE_POOL_CLIENT_KEY、NODE_POOL_CA_CERT 均未配置，无法建立 mTLS 连接")
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("node pool: 加载客户端证书失败: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("node pool: 读取 CA 证书失败: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, errors.New("node pool: CA 证书解析失败，请检查文件格式是否为 PEM")
	}
	tlsConfig.RootCAs = pool

	transport := &http.Transport{TLSClientConfig: tlsConfig}
	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}
	common.SysLog("node pool: mTLS 客户端初始化成功")
	return client, nil
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

	client, err := getNodePoolClient()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": err.Error(),
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

	resp, err := client.Do(req)
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
	nodeName := c.Param("node_name")
	proxyNodePoolRequest(c, fmt.Sprintf("/api/v1/nodes/%s/accounts", nodeName), http.MethodGet)
}

func DeleteNodePoolNode(c *gin.Context) {
	nodeName := c.Param("node_name")
	proxyNodePoolRequest(c, fmt.Sprintf("/api/v1/nodes/%s", nodeName), http.MethodDelete)
}

// end
