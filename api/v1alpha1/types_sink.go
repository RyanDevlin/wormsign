package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ws
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Deliveries",type=integer,JSONPath=`.status.deliveriesTotal`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WormsignSink defines a custom sink for delivering RCA reports to external
// systems via webhook.
type WormsignSink struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the sink configuration.
	Spec WormsignSinkSpec `json:"spec"`

	// Status contains the observed state of the sink.
	// +optional
	Status WormsignSinkStatus `json:"status,omitempty"`
}

// WormsignSinkSpec defines the desired behavior of a custom sink.
type WormsignSinkSpec struct {
	// Description is a human-readable explanation of what this sink does.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description,omitempty"`

	// Type is the sink delivery mechanism. Currently only "webhook" is
	// supported for custom sinks.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=webhook
	Type SinkType `json:"type"`

	// Webhook contains the webhook delivery configuration. Required when
	// type is "webhook".
	// +optional
	Webhook *WebhookConfig `json:"webhook,omitempty"`

	// SeverityFilter restricts which severity levels are delivered to this
	// sink. If empty, all severities are delivered.
	// +optional
	SeverityFilter []Severity `json:"severityFilter,omitempty"`
}

// SinkType specifies the delivery mechanism for a custom sink.
// +kubebuilder:validation:Enum=webhook
type SinkType string

const (
	// SinkTypeWebhook delivers RCA reports via HTTP webhook.
	SinkTypeWebhook SinkType = "webhook"
)

// WebhookConfig contains the configuration for webhook-based sinks.
type WebhookConfig struct {
	// URL is a direct webhook URL. Either URL or URLSecretRef must be
	// specified, but not both. Use URLSecretRef for sensitive URLs.
	// +optional
	URL string `json:"url,omitempty"`

	// URLSecretRef references a Secret key containing the webhook URL.
	// Preferred over URL for sensitive webhook endpoints.
	// +optional
	URLSecretRef *SecretReference `json:"secretRef,omitempty"`

	// Method is the HTTP method to use for the webhook request.
	// +optional
	// +kubebuilder:validation:Enum=POST;PUT;PATCH
	// +kubebuilder:default=POST
	Method string `json:"method,omitempty"`

	// Headers are additional HTTP headers to include in the webhook request.
	// Header values may reference secrets using the syntax
	// ${SECRET:secret-name:key}.
	// +optional
	Headers map[string]string `json:"headers,omitempty"`

	// BodyTemplate is a Go text/template that renders the request body.
	// The template receives the RCA report fields as template variables
	// (e.g., .RootCause, .Severity, .Resource.Kind).
	// +optional
	// +kubebuilder:validation:MaxLength=65536
	BodyTemplate string `json:"bodyTemplate,omitempty"`
}

// WormsignSinkStatus contains the observed state of a WormsignSink.
type WormsignSinkStatus struct {
	WormsignStatus `json:",inline"`

	// DeliveriesTotal is the total number of successful deliveries made by
	// this sink since the controller started.
	// +optional
	DeliveriesTotal int64 `json:"deliveriesTotal,omitempty"`

	// LastDelivery is the timestamp of the most recent delivery attempt.
	// +optional
	LastDelivery *metav1.Time `json:"lastDelivery,omitempty"`

	// LastDeliveryStatus indicates the result of the most recent delivery
	// attempt ("success" or "failure").
	// +optional
	// +kubebuilder:validation:Enum=success;failure
	LastDeliveryStatus string `json:"lastDeliveryStatus,omitempty"`
}

// +kubebuilder:object:root=true

// WormsignSinkList contains a list of WormsignSink resources.
type WormsignSinkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []WormsignSink `json:"items"`
}
