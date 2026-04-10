package app

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

func (a *App) dispatchSlack(evt FluxEvent) error {
	if !a.slack.Enabled {
		log.Printf("slack dispatch disabled")
		return nil
	}

	payload := buildSlackPayload(evt)
	pretty, _ := json.MarshalIndent(payload, "", "  ")

	if a.slack.WebhookURL == "" {
		log.Printf("slack dry-run payload:\n%s", string(pretty))
		return nil
	}

	return a.postJSON(a.slack.WebhookURL, payload, nil)
}

func buildSlackPayload(evt FluxEvent) map[string]any {
	header := fmt.Sprintf("Flux %s: %s/%s", strings.ToUpper(evt.Severity), evt.InvolvedObject.Kind, evt.InvolvedObject.Name)
	revision := evt.Revision()
	if revision == "" {
		revision = "n/a"
	}

	fields := []map[string]any{
		mrkdwnField("*Kind*\n" + evt.InvolvedObject.Kind),
		mrkdwnField("*Namespace*\n" + evt.InvolvedObject.Namespace),
		mrkdwnField("*Name*\n" + evt.InvolvedObject.Name),
		mrkdwnField("*Reason*\n" + evt.Reason),
		mrkdwnField("*Controller*\n" + evt.ReportingController),
		mrkdwnField("*Revision*\n" + revision),
	}

	blocks := []map[string]any{
		{
			"type": "header",
			"text": map[string]string{
				"type": "plain_text",
				"text": header,
			},
		},
		{
			"type":   "section",
			"fields": fields,
		},
		{
			"type": "section",
			"text": map[string]string{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*Message*\n```%s```", truncate(evt.Message, 2500)),
			},
		},
	}

	return map[string]any{
		"text":   header + ": " + singleLine(evt.Message),
		"blocks": blocks,
	}
}

func mrkdwnField(text string) map[string]any {
	return map[string]any{
		"type": "mrkdwn",
		"text": text,
	}
}
