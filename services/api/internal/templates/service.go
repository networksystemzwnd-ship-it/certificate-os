package templates

import (
	"context"
	"strings"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/tenancy"
)

// Service holds the business rules. Permission checks happen in the HTTP layer; the scope and acting
// user always come from the resolved tenant.
type Service struct{ Store Store }

func NewService(s Store) *Service { return &Service{Store: s} }

func event(t tenancy.Tenant, action, requestID string, meta map[string]any) audit.Event {
	return audit.Event{ActorType: audit.ActorUser, ActorUserID: t.UserID, Action: action,
		EntityType: "certificate_template", RequestID: requestID, Metadata: meta}
}

func cfgMeta(c Config) map[string]any {
	return map[string]any{"preset": c.Preset, "orientation": c.Orientation}
}

func (s *Service) Create(ctx context.Context, t tenancy.Tenant, name string, cfg Config, reqID string) (Template, error) {
	name, nerr := NormalizeName(name)
	cfg, cerr := NormalizeConfig(cfg)
	if err := mergeProblems(nerr, cerr); err != nil {
		return Template{}, err
	}
	return s.Store.CreateTemplate(ctx, t.Scope(), name, cfg, t.UserID, event(t, "template.created", reqID, cfgMeta(cfg)))
}

func (s *Service) Get(ctx context.Context, t tenancy.Tenant, id string) (Template, error) {
	return s.Store.GetTemplate(ctx, t.Scope(), id)
}

func (s *Service) List(ctx context.Context, t tenancy.Tenant, p Page) ([]Template, error) {
	p.Q = strings.TrimSpace(p.Q)
	return s.Store.ListTemplates(ctx, t.Scope(), p.Normalized())
}

func (s *Service) Rename(ctx context.Context, t tenancy.Tenant, id, name, reqID string) (Template, error) {
	name, err := NormalizeName(name)
	if err != nil {
		return Template{}, err
	}
	return s.Store.RenameTemplate(ctx, t.Scope(), id, name, event(t, "template.renamed", reqID, nil))
}

func (s *Service) Archive(ctx context.Context, t tenancy.Tenant, id string, archived bool, reqID string) (Template, error) {
	action := "template.archived"
	if !archived {
		action = "template.restored"
	}
	return s.Store.SetArchived(ctx, t.Scope(), id, archived, event(t, action, reqID, nil))
}

// AddVersion validates the configuration and appends it as the template's newest version.
func (s *Service) AddVersion(ctx context.Context, t tenancy.Tenant, id string, cfg Config, reqID string) (Version, error) {
	cfg, err := NormalizeConfig(cfg)
	if err != nil {
		return Version{}, err
	}
	return s.Store.AddVersion(ctx, t.Scope(), id, cfg, t.UserID, event(t, "template.version_created", reqID, cfgMeta(cfg)))
}

func (s *Service) GetVersion(ctx context.Context, t tenancy.Tenant, id string, number int) (Version, error) {
	return s.Store.GetVersion(ctx, t.Scope(), id, number)
}

// PreviewHTML renders a validated draft configuration with sample data.
func (s *Service) PreviewDraft(cfg Config) (string, error) {
	cfg, err := NormalizeConfig(cfg)
	if err != nil {
		return "", err
	}
	return Render(cfg, SampleData, true), nil
}

// PreviewVersion renders a stored version (0 = the current one) with sample data.
func (s *Service) PreviewVersion(ctx context.Context, t tenancy.Tenant, id string, number int) (string, error) {
	if number == 0 {
		tpl, err := s.Store.GetTemplate(ctx, t.Scope(), id)
		if err != nil {
			return "", err
		}
		return Render(tpl.Current.Config, SampleData, true), nil
	}
	v, err := s.Store.GetVersion(ctx, t.Scope(), id, number)
	if err != nil {
		return "", err
	}
	return Render(v.Config, SampleData, true), nil
}

func mergeProblems(errs ...error) error {
	all := map[string]string{}
	for _, e := range errs {
		if ve, ok := e.(*ValidationError); ok {
			for k, v := range ve.Problems {
				all[k] = v
			}
		}
	}
	if len(all) == 0 {
		return nil
	}
	return &ValidationError{Problems: all}
}
