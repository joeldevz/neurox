package observation

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	DefaultNamespace       = "default"
	DefaultObservationType = ObservationTypeDiscovery
	DefaultKind            = KindSemantic
	DefaultConfidence      = 0.7
	LayerBuffer            = 0
)

type ObservationType string

const (
	ObservationTypeDecision   ObservationType = "decision"
	ObservationTypeBugfix     ObservationType = "bugfix"
	ObservationTypeDiscovery  ObservationType = "discovery"
	ObservationTypePattern    ObservationType = "pattern"
	ObservationTypeGotcha     ObservationType = "gotcha"
	ObservationTypeConfig     ObservationType = "config"
	ObservationTypePreference ObservationType = "preference"
	ObservationTypeQuestion   ObservationType = "question"
)

type Kind string

const (
	KindEpisodic   Kind = "episodic"
	KindSemantic   Kind = "semantic"
	KindProcedural Kind = "procedural"
)

type Observation struct {
	ID              string
	Title           string
	Content         string
	ObservationType ObservationType
	Layer           int
	Confidence      float64
	Importance      float64
	Kind            Kind
	Tags            []string
	Namespace       string
	Source          string
	TopicKey        string
	Files           []string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
}

func (o *Observation) ApplyDefaults() {
	if strings.TrimSpace(o.Namespace) == "" {
		o.Namespace = DefaultNamespace
	}
	if strings.TrimSpace(string(o.ObservationType)) == "" {
		o.ObservationType = DefaultObservationType
	}
	if strings.TrimSpace(string(o.Kind)) == "" {
		o.Kind = DefaultKind
	}
	if o.Confidence == 0 {
		o.Confidence = DefaultConfidence
	}
	o.Layer = LayerBuffer
	o.Tags = normalizeList(o.Tags)
	o.Files = normalizeList(o.Files)
	o.TopicKey = strings.TrimSpace(o.TopicKey)
	o.Title = strings.TrimSpace(o.Title)
	o.Content = strings.TrimSpace(o.Content)
}

func (o Observation) Validate() error {
	if o.Title == "" {
		return fmt.Errorf("title is required")
	}
	if o.Content == "" {
		return fmt.Errorf("content is required")
	}
	if err := o.ObservationType.Validate(); err != nil {
		return err
	}
	if err := o.Kind.Validate(); err != nil {
		return err
	}
	if o.Confidence < 0 || o.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	return nil
}

func (t ObservationType) Validate() error {
	switch t {
	case ObservationTypeDecision, ObservationTypeBugfix, ObservationTypeDiscovery, ObservationTypePattern,
		ObservationTypeGotcha, ObservationTypeConfig, ObservationTypePreference, ObservationTypeQuestion:
		return nil
	default:
		return fmt.Errorf("invalid observation_type %q", t)
	}
}

func (k Kind) Validate() error {
	switch k {
	case KindEpisodic, KindSemantic, KindProcedural:
		return nil
	default:
		return fmt.Errorf("invalid kind %q", k)
	}
}

func TagsValue(tags []string) string {
	return strings.Join(normalizeList(tags), ",")
}

func ParseTags(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return normalizeList(strings.Split(value, ","))
}

func normalizeList(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}

	sort.Strings(normalized)
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}
