package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
)

type serveOptions struct {
	addr, db, publicURL, webDir string
	sitesFile, relayAddr        string
	probeState, logRoots        string
	openRestyContainer          string
	offline                     int
	nginx                       managedNginxOptions
}

func newServeFlags(o *serveOptions) *flag.FlagSet {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	offline, _ := strconv.Atoi(os.Getenv("SAILFIN_OFFLINE_SECONDS"))
	flags.StringVar(&o.addr, "addr", os.Getenv("SAILFIN_ADDR"), "Core 监听地址和端口")
	flags.StringVar(&o.db, "db", os.Getenv("SAILFIN_DB"), "SQLite 数据库文件路径")
	flags.StringVar(&o.publicURL, "public-url", os.Getenv("SAILFIN_PUBLIC_URL"), "浏览器访问的管理地址，包含协议、主机和非默认端口")
	flags.StringVar(&o.webDir, "web-dir", os.Getenv("SAILFIN_WEB_DIR"), "自定义管理页面的静态文件目录")
	flags.StringVar(&o.sitesFile, "sites-file", os.Getenv("SAILFIN_SITES_FILE"), "探针站点 JSON 文件路径")
	flags.StringVar(&o.relayAddr, "relay-addr", os.Getenv("SAILFIN_RELAY_ADDR"), "本机探针转发监听地址")
	flags.StringVar(&o.probeState, "probe-state", os.Getenv("SAILFIN_PROBE_STATE"), "探针状态目录")
	flags.StringVar(&o.logRoots, "log-roots", os.Getenv("SAILFIN_LOG_ROOTS"), "允许探针读取的日志目录，多个目录用逗号分隔")
	flags.StringVar(&o.openRestyContainer, "openresty-container", os.Getenv("SAILFIN_OPENRESTY_CONTAINER"), "用于自动接入的 1Panel OpenResty 容器名称")
	flags.IntVar(&o.offline, "offline-seconds", offline, "探针心跳超时秒数")
	flags.StringVar(&o.nginx.upstream, "nginx-upstream", os.Getenv("SAILFIN_NGINX_UPSTREAM"), "独立 Nginx 实例的后端服务地址")
	flags.StringVar(&o.nginx.port, "nginx-port", os.Getenv("SAILFIN_NGINX_PORT"), "独立 Nginx 实例的监听端口")
	flags.StringVar(&o.nginx.id, "nginx-site-id", os.Getenv("SAILFIN_NGINX_SITE_ID"), "独立 Nginx 实例的站点标识")
	flags.StringVar(&o.nginx.name, "nginx-site-name", os.Getenv("SAILFIN_NGINX_SITE_NAME"), "独立 Nginx 实例的站点名称")
	flags.StringVar(&o.nginx.consent, "nginx-require-consent", os.Getenv("SAILFIN_NGINX_REQUIRE_CONSENT"), "独立 Nginx 站点是否等待同意信号（0 或 1）")
	return flags
}

func serveFlags(args []string) (serveOptions, error) {
	var o serveOptions
	flags := newServeFlags(&o)
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if flags.NArg() != 0 {
		return o, fmt.Errorf("unexpected serve argument %q", flags.Arg(0))
	}
	o.nginx.relay = o.relayAddr
	return o, nil
}
