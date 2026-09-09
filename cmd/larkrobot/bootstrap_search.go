package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationindex"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	opensearchschema "github.com/BetaGoRobot/BetaGo-Redefine/script/opensearch"
)

type tenantIndexProvisioner interface {
	EnsureTenantIndex(
		context.Context,
		tenant.Tenant,
		string,
		string,
		[]byte,
	) (opensearch.TenantIndex, error)
}

func newTenantIndexProvisioner() (tenantIndexProvisioner, error) {
	return opensearch.NewProvisioner()
}

func ensureEvaluationSearchIndex(
	ctx context.Context,
	cfg *infraConfig.BaseConfig,
	components *appComponents,
) error {
	if components == nil {
		return errors.New("runtime components are unavailable")
	}
	provisioner, err := components.deps.newTenantIndexProvisioner()
	if err == nil {
		err = provisionEvaluationSearchIndex(ctx, cfg, components, provisioner)
	}
	components.searchBootstrap.Complete(err)
	return err
}

func provisionEvaluationSearchIndex(
	ctx context.Context,
	cfg *infraConfig.BaseConfig,
	components *appComponents,
	provisioner tenantIndexProvisioner,
) error {
	if components == nil || provisioner == nil {
		return errors.New("evaluation search provisioner is unavailable")
	}
	components.evaluationSearchMu.Lock()
	defer components.evaluationSearchMu.Unlock()
	if components.evaluationSearchReady {
		return nil
	}
	evaluationBase := evaluationindex.DefaultIndexAlias
	if cfg != nil && cfg.RuntimeConfig != nil &&
		strings.TrimSpace(cfg.RuntimeConfig.EvaluationIndex) != "" {
		evaluationBase = cfg.RuntimeConfig.EvaluationIndex
	}
	resource, err := provisioner.EnsureTenantIndex(
		ctx,
		components.tenant,
		evaluationBase,
		"conversation_evaluation.v1",
		opensearchschema.ConversationEvaluationsV1,
	)
	if err != nil {
		return err
	}
	components.evaluationSearchReady = true
	components.searchBootstrap.Update(map[string]any{
		"evaluation_alias":    resource.Alias,
		"evaluation_physical": resource.PhysicalIndex,
		"evaluation_schema":   "conversation_evaluation.v1",
	})
	return nil
}

func addSearchSchemaModule(
	app *appruntime.App,
	cfg *infraConfig.BaseConfig,
	components *appComponents,
) {
	app.AddModule(newSearchSchemaModule(cfg, components))
}

func newSearchSchemaModule(
	cfg *infraConfig.BaseConfig,
	components *appComponents,
) appruntime.Module {
	configured := cfg != nil && cfg.OpensearchConfig != nil &&
		strings.TrimSpace(cfg.OpensearchConfig.Domain) != ""
	return appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name: "tenant_search_schema",
		Critical: configured ||
			(components != nil && components.evaluationSettings.Enabled()),
		Start: func(ctx context.Context) (startErr error) {
			if components != nil && components.searchBootstrap != nil {
				defer func() {
					components.searchBootstrap.Complete(startErr)
				}()
			}
			if !configured {
				if components != nil && components.evaluationSettings.Enabled() {
					return errors.New(
						"conversation evaluation requires opensearch configuration",
					)
				}
				return appruntime.ErrDisabled
			}
			if components == nil {
				return errors.New("runtime components are unavailable")
			}
			provisioner, err := components.deps.newTenantIndexProvisioner()
			if err != nil {
				return err
			}
			conversationResource, err := provisioner.EnsureTenantIndex(
				ctx,
				components.tenant,
				appruntime.ConversationEventIndex(cfg),
				"conversation_event.v1",
				opensearchschema.ConversationEventsV1,
			)
			if err != nil {
				return fmt.Errorf("provision conversation event index: %w", err)
			}
			components.searchBootstrap.Update(map[string]any{
				"conversation_alias":    conversationResource.Alias,
				"conversation_physical": conversationResource.PhysicalIndex,
			})
			if !components.evaluationSettings.Enabled() {
				return nil
			}
			if err := provisionEvaluationSearchIndex(
				ctx,
				cfg,
				components,
				provisioner,
			); err != nil {
				return fmt.Errorf("provision evaluation index: %w", err)
			}
			return nil
		},
		Stats: func() map[string]any {
			if components == nil {
				return nil
			}
			return components.searchBootstrap.Stats()
		},
	})
}
