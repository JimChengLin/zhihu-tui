package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	Version = "0.2.4-go"

	ZhihuBaseURL           = "https://www.zhihu.com"
	ZhihuAPIV4             = "https://www.zhihu.com/api/v4"
	ZhihuAPIV3             = "https://www.zhihu.com/api/v3"
	ZhihuZhuanlanAPI       = "https://zhuanlan.zhihu.com/api"
	ZhihuImageAPI          = "https://api.zhihu.com/images"
	ZhihuOSSUploadURL      = "https://zhihu-pics-upload.zhimg.com"
	ZhihuLoginURL          = ZhihuBaseURL + "/signin"
	ZhihuQRCodeAPI         = ZhihuAPIV3 + "/account/api/login/qrcode"
	ZhihuOAuthCaptcha      = ZhihuAPIV3 + "/oauth/captcha/v2?type=captcha_sign_in"
	ZhihuContentPublishURL = ZhihuAPIV4 + "/content/publish"
	ZhihuContentDraftsURL  = ZhihuAPIV4 + "/content/drafts"

	ChromeVersion    = "145"
	DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + ChromeVersion + ".0.0.0 Safari/537.36"
)

var DefaultTimeout = 15 * time.Second

var RequiredCookies = map[string]struct{}{
	"z_c0":  {},
	"_xsrf": {},
	"d_c0":  {},
}

func ConfigDir() (string, error) {
	if dir := os.Getenv("ZHIHU_CLI_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".zhihu-cli"), nil
}

func CookieFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cookies.json"), nil
}

func QRCodeTextFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "login_qrcode.txt"), nil
}

func BrowserHeaders() map[string]string {
	return map[string]string{
		"User-Agent":         DefaultUserAgent,
		"Accept":             "application/json, text/plain, */*",
		"Accept-Language":    "en-US,en;q=0.9",
		"Referer":            ZhihuBaseURL + "/",
		"sec-ch-ua":          `"Not:A-Brand";v="99", "Google Chrome";v="` + ChromeVersion + `", "Chromium";v="` + ChromeVersion + `"`,
		"sec-ch-ua-mobile":   "?0",
		"sec-ch-ua-platform": `"Windows"`,
	}
}
