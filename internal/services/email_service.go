package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type EmailService struct {
	apiKey    string
	fromEmail string
	enabled   bool
}

func NewEmailService(apiKey, fromEmail string) *EmailService {
	enabled := apiKey != "" && fromEmail != ""
	return &EmailService{
		apiKey:    apiKey,
		fromEmail: fromEmail,
		enabled:   enabled,
	}
}

func (s *EmailService) IsEnabled() bool {
	return s.enabled
}

func (s *EmailService) SendActivationEmail(ctx context.Context, toEmail, code string) error {
	if !s.enabled {
		fmt.Printf("[EmailService] Email disabled, would send activation code %s to %s\n", code, toEmail)
		return nil
	}
	
	fmt.Printf("[EmailService] Sending activation code %s to %s via Resend API\n", code, toEmail)

	subject := "【Monera Digital】账号激活验证码"
	htmlContent := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <style>
    body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px; }
    .header { background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); color: white; padding: 30px; text-align: center; border-radius: 10px 10px 0 0; }
    .content { background: #f9f9f9; padding: 30px; border-radius: 0 0 10px 10px; }
    .code { font-size: 36px; font-weight: bold; letter-spacing: 12px; color: #2563eb; 
            background: white; padding: 20px 40px; border-radius: 8px; 
            border: 2px solid #e5e7eb; text-align: center; margin: 20px 0; }
    .info { background: white; padding: 15px; border-radius: 8px; margin: 15px 0; }
    .info-item { margin: 8px 0; color: #666; }
    .warning { background: #fef3cd; border-left: 4px solid #ffc107; padding: 15px; border-radius: 4px; margin: 20px 0; }
    .footer { margin-top: 30px; padding-top: 20px; border-top: 1px solid #e5e7eb; color: #888; font-size: 14px; text-align: center; }
  </style>
</head>
<body>
  <div class="header">
    <h1>Monera Digital</h1>
  </div>
  <div class="content">
    <h2>尊敬的 Monera Digital 用户：</h2>
    <p>您好！</p>
    <p>您的账号激活验证码为：</p>
    <div class="code">%s</div>
    <div class="info">
      <div class="info-item"><strong>验证码有效期：</strong>5 分钟</div>
      <div class="info-item"><strong>可尝试次数：</strong>5 次</div>
    </div>
    <div class="warning">
      <strong>安全提示：</strong>如果这不是您本人的操作，请忽略此邮件。任何人索取此验证码都可能是诈骗行为。
    </div>
    <div class="footer">
      <p>此致</p>
      <p><strong>Monera Digital 团队</strong></p>
      <p>发送时间：%s</p>
    </div>
  </div>
</body>
</html>
`, code, time.Now().Format("2006-01-02 15:04:05"))

	plainText := fmt.Sprintf(`
尊敬的 Monera Digital 用户：

您好！

您的账号激活验证码为：%s

验证码有效期：5 分钟
验证码可尝试次数：5 次

如果这不是您本人的操作，请忽略此邮件。

此致
Monera Digital 团队
发送时间：%s
`, code, time.Now().Format("2006-01-02 15:04:05"))

	return s.sendEmail(ctx, toEmail, subject, plainText, htmlContent)
}

func (s *EmailService) sendEmail(ctx context.Context, to, subject, plainText, htmlContent string) error {
	apiURL := "https://api.resend.com/emails"

	payload := map[string]interface{}{
		"from":    s.fromEmail,
		"to":      []string{to},
		"subject": subject,
		"text":    plainText,
		"html":    htmlContent,
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal email payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("[EmailService] Failed to send email to %s: %v\n", to, err)
		return fmt.Errorf("failed to send email: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("[EmailService] Email sent successfully to %s, status: %d\n", to, resp.StatusCode)

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("[EmailService] Email API error: status %d, body: %s\n", resp.StatusCode, string(body))
		return fmt.Errorf("email API returned status %d", resp.StatusCode)
	}

	return nil
}

func (s *EmailService) GetFromEmail() string {
	return s.fromEmail
}

func (s *EmailService) GetEnvFromEmail() string {
	return os.Getenv("SENDER_EMAIL")
}

func (s *EmailService) SendContactInfoSubmittedEmail(ctx context.Context, toEmail string) error {
	if !s.enabled {
		fmt.Printf("[EmailService] Email disabled, would send contact info submitted notification to %s\n", toEmail)
		return nil
	}

	subject := "【Monera Digital】联系方式已提交"
	htmlContent := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <style>
    body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px; }
    .header { background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); color: white; padding: 30px; text-align: center; border-radius: 10px 10px 0 0; }
    .content { background: #f9f9f9; padding: 30px; border-radius: 0 0 10px 10px; }
    .success-icon { font-size: 48px; text-align: center; margin: 20px 0; }
    .info { background: white; padding: 15px; border-radius: 8px; margin: 15px 0; }
    .info-item { margin: 8px 0; color: #666; }
    .footer { margin-top: 30px; padding-top: 20px; border-top: 1px solid #e5e7eb; color: #888; font-size: 14px; text-align: center; }
  </style>
</head>
<body>
  <div class="header">
    <h1>Monera Digital</h1>
  </div>
  <div class="content">
    <h2>尊敬的 Monera Digital 用户：</h2>
    <p>您好！</p>
    <div class="success-icon">✓</div>
    <p style="text-align: center; color: #22c55e; font-size: 18px;"><strong>您的联系方式已成功提交！</strong></p>
    <div class="info">
      <p><strong>审核说明：</strong></p>
      <p>我们将在 <strong>1-3 个工作日</strong>内完成审核。</p>
      <p>审核通过后，您将收到账号激活成功的邮件通知。</p>
    </div>
    <div class="info">
      <p><strong>如有疑问，请联系：</strong></p>
      <p><a href="mailto:ops@moneradigital.com">ops@moneradigital.com</a></p>
    </div>
    <div class="footer">
      <p>此致</p>
      <p><strong>Monera Digital 团队</strong></p>
      <p>发送时间：%s</p>
    </div>
  </div>
</body>
</html>
`, time.Now().Format("2006-01-02 15:04:05"))

	plainText := fmt.Sprintf(`
尊敬的 Monera Digital 用户：

您好！

您的联系方式已成功提交！

审核说明：
- 我们将在 1-3 个工作日内完成审核
- 审核通过后，您将收到账号激活成功的邮件通知

如有疑问，请联系：ops@moneradigital.com

此致
Monera Digital 团队
发送时间：%s
`, time.Now().Format("2006-01-02 15:04:05"))

	return s.sendEmail(ctx, toEmail, subject, plainText, htmlContent)
}

func (s *EmailService) SendAccountActivatedEmail(ctx context.Context, toEmail string) error {
	if !s.enabled {
		fmt.Printf("[EmailService] Email disabled, would send account activated notification to %s\n", toEmail)
		return nil
	}

	subject := "【Monera Digital】您的账号已成功激活"
	htmlContent := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <style>
    body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px; }
    .header { background: linear-gradient(135deg, #22c55e 0%%, #16a34a 100%%); color: white; padding: 30px; text-align: center; border-radius: 10px 10px 0 0; }
    .content { background: #f9f9f9; padding: 30px; border-radius: 0 0 10px 10px; }
    .success-icon { font-size: 64px; text-align: center; margin: 20px 0; }
    .info { background: white; padding: 15px; border-radius: 8px; margin: 15px 0; }
    .info-item { margin: 8px 0; color: #666; }
    .cta-button { display: inline-block; background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); color: white; padding: 15px 40px; border-radius: 8px; text-decoration: none; font-weight: bold; margin: 20px 0; }
    .footer { margin-top: 30px; padding-top: 20px; border-top: 1px solid #e5e7eb; color: #888; font-size: 14px; text-align: center; }
  </style>
</head>
<body>
  <div class="header">
    <h1>🎉 账号激活成功</h1>
  </div>
  <div class="content">
    <h2>尊敬的 Monera Digital 用户：</h2>
    <p>您好！</p>
    <div class="success-icon">✓</div>
    <p style="text-align: center; color: #22c55e; font-size: 20px;"><strong>恭喜！您的账号已审核通过，现在可以正常登录使用了！</strong></p>
    <div class="info">
      <p><strong>账号信息：</strong></p>
      <p><strong>登录邮箱：</strong>%s</p>
      <p><strong>激活时间：</strong>%s</p>
    </div>
    <div style="text-align: center;">
      <a href="#" class="cta-button">立即登录</a>
    </div>
    <div class="info">
      <p><strong>如有疑问，请联系：</strong></p>
      <p><a href="mailto:ops@moneradigital.com">ops@moneradigital.com</a></p>
    </div>
    <div class="footer">
      <p>感谢您的耐心等待！</p>
      <p><strong>Monera Digital 团队</strong></p>
      <p>发送时间：%s</p>
    </div>
  </div>
</body>
</html>
`, toEmail, time.Now().Format("2006-01-02 15:04:05"), time.Now().Format("2006-01-02 15:04:05"))

	plainText := fmt.Sprintf(`
尊敬的 Monera Digital 用户：

您好！

恭喜！您的账号已审核通过，现在可以正常登录使用了！

账号信息：
- 登录邮箱：%s
- 激活时间：%s

如有疑问，请联系：ops@moneradigital.com

感谢您的耐心等待！

Monera Digital 团队
发送时间：%s
`, toEmail, time.Now().Format("2006-01-02 15:04:05"), time.Now().Format("2006-01-02 15:04:05"))

	return s.sendEmail(ctx, toEmail, subject, plainText, htmlContent)
}
