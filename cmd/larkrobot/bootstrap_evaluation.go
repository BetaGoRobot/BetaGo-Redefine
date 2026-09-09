package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationindex"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationwindow"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	uuid "github.com/satori/go.uuid"
)

func addConversationEvaluationModule(
	app *appruntime.App,
	cfg *infraConfig.BaseConfig,
	components *appComponents,
) {
	var candidateWorker *conversationeval.CandidateWorker
	var judgeWorker *conversationeval.JudgeWorker
	var projectionWorker *evaluationindex.ProjectionWorker
	var repository *evaluationstore.Repository
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "conversation_evaluation",
		Critical: components.evaluationSettings.Enabled(),
		Start: func(ctx context.Context) error {
			owner, err := tenant.New(
				cfg.LarkConfig.AppID,
				cfg.LarkConfig.BotOpenID,
			)
			if err != nil {
				return fmt.Errorf("derive evaluation tenant: %w", err)
			}
			repository, err = evaluationstore.NewRepository(db.DB(), owner)
			if err != nil {
				return fmt.Errorf("create evaluation repository: %w", err)
			}
			service, err := conversationeval.NewService(conversationeval.ServiceOptions{
				Repository: repository, PreWindowSource: evaluationwindow.OpenSearchPreWindowSource{},
				CandidateSubmitter: repository,
				EnsureCohortForChat: func(chatID string) bool {
					return components.agenticRollouts.EvaluationEnabled(
						context.Background(),
						chatID,
					)
				},
				CohortDuration: components.evaluationSettings.CohortDuration,
			})
			if err != nil {
				return err
			}
			runtimeConfig := cfg.RuntimeConfig
			if runtimeConfig == nil {
				runtimeConfig = &infraConfig.RuntimeConfig{}
			}
			candidateRunnerFactory, err := handlers.NewCandidateRunnerFactory(
				evaluationCandidateModelID(cfg, runtimeConfig),
			)
			if err != nil {
				return fmt.Errorf("create evaluation candidate runner factory: %w", err)
			}
			processor, err := conversationeval.NewCandidateProcessor(
				repository,
				service,
				candidateRunnerFactory,
				conversationeval.CandidateProcessorConfig{
					WorkerID: "evaluation-candidate-" + uuid.NewV4().String(),
					LeaseTTL: durationSeconds(
						runtimeConfig.EvaluationCandidateLeaseSeconds,
						10*time.Minute,
					),
					RetryDelay: durationSeconds(
						runtimeConfig.EvaluationCandidateRetrySeconds,
						15*time.Second,
					),
				},
			)
			if err != nil {
				return err
			}
			pollInterval := time.Second
			if runtimeConfig.EvaluationCandidatePollMillis > 0 {
				pollInterval = time.Duration(runtimeConfig.EvaluationCandidatePollMillis) *
					time.Millisecond
			}
			candidateWorker, err = conversationeval.NewCandidateWorker(
				processor,
				conversationeval.CandidateWorkerOptions{
					Workers:  runtimeConfig.EvaluationCandidateWorkers,
					Interval: pollInterval,
					WindowSweepInterval: durationSeconds(
						runtimeConfig.EvaluationWindowSweepSeconds,
						5*time.Second,
					),
				},
			)
			if err != nil {
				return err
			}
			judgeModelID := ""
			if !runtimeConfig.EvaluationJudgeDisabled {
				judgeModelID = evaluationJudgeModelID(cfg, runtimeConfig)
			}
			if judgeModelID != "" {
				judge, judgeErr := conversationeval.NewJudge(
					conversationeval.JudgeConfig{ModelID: judgeModelID},
					repository,
				)
				if judgeErr != nil {
					return judgeErr
				}
				judgeProcessor, judgeErr := conversationeval.NewJudgeProcessor(
					repository,
					judge,
					nil,
				)
				if judgeErr != nil {
					return judgeErr
				}
				judgePollInterval := time.Second
				if runtimeConfig.EvaluationJudgePollMillis > 0 {
					judgePollInterval = time.Duration(
						runtimeConfig.EvaluationJudgePollMillis,
					) * time.Millisecond
				}
				judgeWorker, judgeErr = conversationeval.NewJudgeWorker(
					judgeProcessor,
					conversationeval.JudgeWorkerOptions{
						Workers:    runtimeConfig.EvaluationJudgeWorkers,
						Interval:   judgePollInterval,
						MaxBackoff: 2 * time.Minute,
					},
				)
				if judgeErr != nil {
					return judgeErr
				}
			}
			if cfg.OpensearchConfig != nil {
				indexStore, indexErr := evaluationindex.NewStoreWithBackend(
					components.tenant,
					components.evaluationIndexAlias,
					evaluationindex.NewOpenSearchBackend(),
				)
				if indexErr != nil {
					return indexErr
				}
				projectionProcessor, indexErr := evaluationindex.NewProjectionProcessor(
					repository,
					indexStore,
					runtimeConfig.EvaluationProjectionBatchSize,
				)
				if indexErr != nil {
					return indexErr
				}
				projectionWorker, indexErr = evaluationindex.NewProjectionWorker(
					projectionProcessor,
					evaluationindex.ProjectionWorkerOptions{
						Interval: durationSeconds(
							runtimeConfig.EvaluationProjectionIntervalSeconds,
							30*time.Second,
						),
						MaxBackoff: 5 * time.Minute,
					},
				)
				if indexErr != nil {
					return indexErr
				}
			}
			components.messageProcessor.SetEvaluationService(service)
			components.feedbackRouter.Bind(service)
			if err := candidateWorker.Start(ctx); err != nil {
				components.feedbackRouter.Bind(nil)
				components.messageProcessor.SetEvaluationService(nil)
				return err
			}
			if judgeWorker != nil {
				if err := judgeWorker.Start(ctx); err != nil {
					_ = candidateWorker.Stop(ctx)
					components.feedbackRouter.Bind(nil)
					components.messageProcessor.SetEvaluationService(nil)
					return err
				}
			}
			if projectionWorker != nil {
				if err := projectionWorker.Start(ctx); err != nil {
					if judgeWorker != nil {
						_ = judgeWorker.Stop(ctx)
					}
					_ = candidateWorker.Stop(ctx)
					components.feedbackRouter.Bind(nil)
					components.messageProcessor.SetEvaluationService(nil)
					return err
				}
			}
			return nil
		},
		Ready: func(context.Context) error {
			if candidateWorker == nil {
				return errors.New("conversation evaluation worker is not running")
			}
			return nil
		},
		Stop: func(ctx context.Context) error {
			components.feedbackRouter.Bind(nil)
			components.messageProcessor.SetEvaluationService(nil)
			var stopErr error
			if projectionWorker != nil {
				stopErr = errors.Join(stopErr, projectionWorker.Stop(ctx))
			}
			if judgeWorker != nil {
				stopErr = errors.Join(stopErr, judgeWorker.Stop(ctx))
			}
			if candidateWorker != nil {
				stopErr = errors.Join(stopErr, candidateWorker.Stop(ctx))
			}
			return stopErr
		},
		Stats: func() map[string]any {
			base := map[string]any{
				"tenant_id":       components.tenant.ID,
				"evaluation_mode": string(components.evaluationSettings.Mode),
				"allow_count":     components.evaluationSettings.AllowedChatCount(),
			}
			if candidateWorker == nil {
				base["running"] = false
				return base
			}
			stats := base
			stats["running"] = true
			stats["candidate"] = candidateWorker.Stats()
			stats["judge_enabled"] = judgeWorker != nil
			stats["projection_enabled"] = projectionWorker != nil
			if judgeWorker != nil {
				stats["judge"] = judgeWorker.Stats()
			}
			if projectionWorker != nil {
				stats["projection"] = projectionWorker.Stats()
			}
			if repository != nil {
				metricsCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				cursor := evaluationindex.ProjectionCursor{}
				if projectionWorker != nil {
					cursor = projectionWorker.Cursor()
				}
				if metrics, err := repository.EvaluationMetrics(metricsCtx, cursor); err != nil {
					stats["metrics_error"] = err.Error()
				} else {
					stats["metrics"] = metrics
				}
			}
			return stats
		},
	}))
}

func evaluationJudgeModelID(
	cfg *infraConfig.BaseConfig,
	runtimeConfig *infraConfig.RuntimeConfig,
) string {
	if runtimeConfig != nil {
		if modelID := strings.TrimSpace(runtimeConfig.EvaluationJudgeModel); modelID != "" {
			return modelID
		}
	}
	if cfg == nil || cfg.ArkConfig == nil {
		return ""
	}
	if modelID := strings.TrimSpace(cfg.ArkConfig.ReasoningModel); modelID != "" {
		return modelID
	}
	return strings.TrimSpace(cfg.ArkConfig.NormalModel)
}

func evaluationCandidateModelID(
	cfg *infraConfig.BaseConfig,
	runtimeConfig *infraConfig.RuntimeConfig,
) string {
	if runtimeConfig != nil {
		if modelID := strings.TrimSpace(runtimeConfig.EvaluationCandidateModel); modelID != "" {
			return modelID
		}
	}
	if cfg == nil || cfg.ArkConfig == nil {
		return ""
	}
	if modelID := strings.TrimSpace(cfg.ArkConfig.ReasoningModel); modelID != "" {
		return modelID
	}
	return strings.TrimSpace(cfg.ArkConfig.NormalModel)
}

func durationSeconds(value int, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Second
}
