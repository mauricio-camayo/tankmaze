// Package main implements ops-notifier (item 278) — a thin fan-out worker
// subscribed to the shared OpsAlertTopic SNS topic (api-stack.ts) that every
// ops CloudWatch Alarm in this project already publishes to. CloudWatch
// Alarm → SNS messages carry both ALARM and OK state transitions, so this
// one Lambda covers "fail" and "recover" notifications for every alarm on
// the topic, not just the new SeriesMaterializerCarryForwardFailureAlarm
// this item was written for.
//
// Delivery is SES first, falling back to ntfy only if the SES send itself
// errors or SES isn't configured. Both channels are optional and
// independently gated by their own env vars (mirroring the existing
// "log and skip" pattern in forgot-password-worker for an unconfigured SES
// sender) — this worker never returns an error back to SNS, since a
// notification-delivery failure retrying forever would just spam both
// channels once they recover, and there is no further fallback channel to
// escalate to.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
)

type handler struct {
	ses        *ses.Client
	httpClient *http.Client
	sesSender  string // "" disables SES delivery
	opsEmail   string // "" disables SES delivery (nothing to send to)
	ntfyURL    string // e.g. "https://ntfy.sh" or a self-hosted base URL — "" disables ntfy
	ntfyTopic  string // "" disables ntfy
}

var h *handler

func main() {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	h = &handler{
		ses:        ses.NewFromConfig(cfg),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		sesSender:  os.Getenv("SES_SENDER_EMAIL"),
		opsEmail:   os.Getenv("OPS_ALERT_EMAIL"),
		ntfyURL:    os.Getenv("NTFY_URL"),
		ntfyTopic:  os.Getenv("NTFY_TOPIC"),
	}
	lambda.Start(h.handle)
}

// cloudWatchAlarmMessage is the subset of fields CloudWatch puts in the body
// of the SNS message it publishes on an alarm state change. See:
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/US_SupportedActions.html
type cloudWatchAlarmMessage struct {
	AlarmName        string `json:"AlarmName"`
	AlarmDescription string `json:"AlarmDescription"`
	NewStateValue    string `json:"NewStateValue"` // "ALARM" | "OK" | "INSUFFICIENT_DATA"
	NewStateReason   string `json:"NewStateReason"`
	Region           string `json:"Region"`
}

func (h *handler) handle(ctx context.Context, ev events.SNSEvent) error {
	for _, record := range ev.Records {
		h.notify(ctx, record.SNS.Message)
	}
	return nil
}

func (h *handler) notify(ctx context.Context, rawMessage string) {
	var msg cloudWatchAlarmMessage
	subject := "TankMaze ops alert"
	body := rawMessage
	if err := json.Unmarshal([]byte(rawMessage), &msg); err == nil && msg.AlarmName != "" {
		switch msg.NewStateValue {
		case "OK":
			subject = fmt.Sprintf("✅ RECOVERED: %s", msg.AlarmName)
		case "ALARM":
			subject = fmt.Sprintf("🔴 ALARM: %s", msg.AlarmName)
		default:
			subject = fmt.Sprintf("%s: %s", msg.NewStateValue, msg.AlarmName)
		}
		body = fmt.Sprintf("%s\n\nRegion: %s\n\n%s", msg.AlarmDescription, msg.Region, msg.NewStateReason)
	}

	if err := h.sendSES(ctx, subject, body); err != nil {
		log.Printf("ops-notifier: SES send failed, falling back to ntfy: %v", err)
		if err := h.sendNtfy(ctx, subject, body); err != nil {
			log.Printf("ops-notifier: ntfy send also failed: %v", err)
		}
		return
	}
}

// sendSES returns an error (rather than logging and swallowing) specifically
// so notify's caller can tell "SES isn't configured" / "SES call failed"
// apart from "delivered" and fall back to ntfy in both cases.
func (h *handler) sendSES(ctx context.Context, subject, body string) error {
	if h.sesSender == "" || h.opsEmail == "" {
		return fmt.Errorf("SES sender or destination not configured")
	}
	_, err := h.ses.SendEmail(ctx, &ses.SendEmailInput{
		Source: aws.String(h.sesSender),
		Destination: &sestypes.Destination{
			ToAddresses: []string{h.opsEmail},
		},
		Message: &sestypes.Message{
			Subject: &sestypes.Content{Data: aws.String(subject)},
			Body: &sestypes.Body{
				Text: &sestypes.Content{Data: aws.String(body)},
			},
		},
	})
	return err
}

func (h *handler) sendNtfy(ctx context.Context, subject, body string) error {
	if h.ntfyURL == "" || h.ntfyTopic == "" {
		return fmt.Errorf("ntfy URL or topic not configured")
	}
	url := fmt.Sprintf("%s/%s", h.ntfyURL, h.ntfyTopic)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", subject)
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy returned HTTP %d", resp.StatusCode)
	}
	return nil
}
