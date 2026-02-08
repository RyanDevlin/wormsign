package prompt

import (
	"fmt"
	"strings"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// Builder constructs LLM prompts by combining the system prompt with
// diagnostic bundle data. It supports user overrides and appends.
type Builder struct {
	systemPrompt string
}

// NewBuilder creates a prompt Builder. If systemPromptOverride is non-empty,
// it replaces the default system prompt entirely. systemPromptAppend is
// appended after the system prompt (default or override).
func NewBuilder(systemPromptOverride, systemPromptAppend string) *Builder {
	base := DefaultSystemPrompt
	if strings.TrimSpace(systemPromptOverride) != "" {
		base = systemPromptOverride
	}
	if strings.TrimSpace(systemPromptAppend) != "" {
		base = base + "\n\n" + systemPromptAppend
	}
	return &Builder{systemPrompt: base}
}

// SystemPrompt returns the fully constructed system prompt.
func (b *Builder) SystemPrompt() string {
	return b.systemPrompt
}

// BuildUserPrompt formats a DiagnosticBundle into the user message sent
// to the LLM alongside the system prompt.
func (b *Builder) BuildUserPrompt(bundle model.DiagnosticBundle) string {
	var sb strings.Builder

	sb.WriteString("# Diagnostic Bundle\n\n")

	// Write fault event or super-event header.
	if bundle.SuperEvent != nil {
		se := bundle.SuperEvent
		sb.WriteString("## Super-Event (Correlated)\n\n")
		sb.WriteString(fmt.Sprintf("- **Correlation Rule:** %s\n", se.CorrelationRule))
		sb.WriteString(fmt.Sprintf("- **Primary Resource:** %s\n", se.PrimaryResource.String()))
		sb.WriteString(fmt.Sprintf("- **Severity:** %s\n", se.Severity))
		sb.WriteString(fmt.Sprintf("- **Timestamp:** %s\n", se.Timestamp.UTC().Format("2006-01-02T15:04:05Z")))
		sb.WriteString(fmt.Sprintf("- **Correlated Fault Events:** %d\n\n", len(se.FaultEvents)))

		for i, fe := range se.FaultEvents {
			sb.WriteString(fmt.Sprintf("### Fault Event %d\n\n", i+1))
			writeFaultEvent(&sb, &fe)
		}
	} else if bundle.FaultEvent != nil {
		sb.WriteString("## Fault Event\n\n")
		writeFaultEvent(&sb, bundle.FaultEvent)
	}

	// Write diagnostic sections.
	if len(bundle.Sections) > 0 {
		sb.WriteString("## Diagnostic Data\n\n")
		for _, section := range bundle.Sections {
			sb.WriteString(fmt.Sprintf("### %s\n\n", section.Title))
			if section.Error != "" {
				sb.WriteString(fmt.Sprintf("**Error gathering data:** %s\n\n", section.Error))
			}
			if section.Content != "" {
				if section.Format == "json" || section.Format == "yaml" {
					sb.WriteString(fmt.Sprintf("```%s\n%s\n```\n\n", section.Format, section.Content))
				} else {
					sb.WriteString(section.Content)
					sb.WriteString("\n\n")
				}
			}
		}
	}

	return sb.String()
}

// writeFaultEvent writes a formatted fault event to the string builder.
func writeFaultEvent(sb *strings.Builder, fe *model.FaultEvent) {
	sb.WriteString(fmt.Sprintf("- **Detector:** %s\n", fe.DetectorName))
	sb.WriteString(fmt.Sprintf("- **Severity:** %s\n", fe.Severity))
	sb.WriteString(fmt.Sprintf("- **Resource:** %s\n", fe.Resource.String()))
	sb.WriteString(fmt.Sprintf("- **Timestamp:** %s\n", fe.Timestamp.UTC().Format("2006-01-02T15:04:05Z")))
	if fe.Description != "" {
		sb.WriteString(fmt.Sprintf("- **Description:** %s\n", fe.Description))
	}
	if len(fe.Labels) > 0 {
		sb.WriteString("- **Labels:**\n")
		for k, v := range fe.Labels {
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", k, v))
		}
	}
	if len(fe.Annotations) > 0 {
		sb.WriteString("- **Annotations:**\n")
		for k, v := range fe.Annotations {
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", k, v))
		}
	}
	sb.WriteString("\n")
}
