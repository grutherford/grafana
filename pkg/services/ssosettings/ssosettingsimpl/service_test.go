package ssosettingsimpl

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gopkg.in/ini.v1"

	"github.com/grafana/grafana/pkg/api/routing"
	"github.com/grafana/grafana/pkg/infra/db/dbtest"
	"github.com/grafana/grafana/pkg/infra/usagestats"
	"github.com/grafana/grafana/pkg/login/social"
	"github.com/grafana/grafana/pkg/services/accesscontrol"
	"github.com/grafana/grafana/pkg/services/accesscontrol/acimpl"
	"github.com/grafana/grafana/pkg/services/featuremgmt"
	"github.com/grafana/grafana/pkg/services/licensing/licensingtest"
	secretsFakes "github.com/grafana/grafana/pkg/services/secrets/fakes"
	"github.com/grafana/grafana/pkg/services/ssosettings"
	"github.com/grafana/grafana/pkg/services/ssosettings/models"
	"github.com/grafana/grafana/pkg/services/ssosettings/ssosettingstests"
	"github.com/grafana/grafana/pkg/services/user"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/tests/testsuite"
)

func TestMain(m *testing.M) {
	testsuite.Run(m)
}

func TestService_GetForProvider(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		setup   func(env testEnv)
		want    *models.SSOSettings
		wantErr bool
	}{
		{
			name: "should return successfully",
			setup: func(env testEnv) {
				env.store.ExpectedSSOSetting = &models.SSOSettings{
					Provider: "github",
					Settings: map[string]any{"enabled": true},
					Source:   models.DB,
				}
				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedConfigs = map[string]map[string]any{
					"github": {
						"client_id":     "client_id",
						"client_secret": "secret",
					},
				}
			},
			want: &models.SSOSettings{
				Provider: "github",
				Settings: map[string]any{
					"enabled":       true,
					"client_id":     "client_id",
					"client_secret": "secret",
				},
			},
			wantErr: false,
		},
		{
			name:    "should return error if store returns an error different than not found",
			setup:   func(env testEnv) { env.store.ExpectedError = fmt.Errorf("error") },
			want:    nil,
			wantErr: true,
		},
		{
			name: "should fallback to the system settings if store returns not found",
			setup: func(env testEnv) {
				env.store.ExpectedError = ssosettings.ErrNotFound
				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedConfigs = map[string]map[string]any{
					"github": {
						"enabled":   true,
						"client_id": "client_id",
					},
				}
			},
			want: &models.SSOSettings{
				Provider: "github",
				Settings: map[string]any{
					"enabled":   true,
					"client_id": "client_id"},
				Source: models.System,
			},
			wantErr: false,
		},
		{
			name: "should return error if the fallback strategy was not found",
			setup: func(env testEnv) {
				env.store.ExpectedError = ssosettings.ErrNotFound
				env.fallbackStrategy.ExpectedIsMatch = false
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "should return error if fallback strategy returns error",
			setup: func(env testEnv) {
				env.store.ExpectedError = ssosettings.ErrNotFound
				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedError = fmt.Errorf("error")
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "should decrypt secrets if data is coming from store",
			setup: func(env testEnv) {
				env.store.ExpectedSSOSetting = &models.SSOSettings{
					Provider: "github",
					Settings: map[string]any{
						"enabled":       true,
						"client_secret": base64.RawStdEncoding.EncodeToString([]byte("client_secret")),
						"other_secret":  base64.RawStdEncoding.EncodeToString([]byte("other_secret")),
					},
					Source: models.DB,
				}
				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedConfigs = map[string]map[string]any{
					"github": {
						"client_id": "client_id",
					},
				}

				env.secrets.On("Decrypt", mock.Anything, []byte("client_secret"), mock.Anything).Return([]byte("decrypted-client-secret"), nil).Once()
				env.secrets.On("Decrypt", mock.Anything, []byte("other_secret"), mock.Anything).Return([]byte("decrypted-other-secret"), nil).Once()
			},
			want: &models.SSOSettings{
				Provider: "github",
				Settings: map[string]any{
					"enabled":       true,
					"client_id":     "client_id",
					"client_secret": "decrypted-client-secret",
					"other_secret":  "decrypted-other-secret",
				},
				Source: models.DB,
			},
			wantErr: false,
		},
		{
			name: "should not decrypt secrets if data is coming from the fallback strategy",
			setup: func(env testEnv) {
				env.store.ExpectedError = ssosettings.ErrNotFound
				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedConfigs = map[string]map[string]any{
					"github": {
						"enabled":       true,
						"client_id":     "client_id",
						"client_secret": "client_secret",
					},
				}
			},
			want: &models.SSOSettings{
				Provider: "github",
				Settings: map[string]any{
					"enabled":       true,
					"client_id":     "client_id",
					"client_secret": "client_secret",
				},
				Source: models.System,
			},
			wantErr: false,
		},
		{
			name: "should return an error if the data in the store is invalid",
			setup: func(env testEnv) {
				env.store.ExpectedSSOSetting = &models.SSOSettings{
					Provider: "github",
					Settings: map[string]any{
						"enabled":       true,
						"client_secret": "not a valid base64 string",
					},
					Source: models.DB,
				}
				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedConfigs = map[string]map[string]any{
					"github": {
						"client_id": "client_id",
					},
				}
			},
			wantErr: true,
		},
		{
			name: "correctly merge the DB and system settings",
			setup: func(env testEnv) {
				env.store.ExpectedSSOSetting = &models.SSOSettings{
					Provider: "github",
					Settings: map[string]any{
						"enabled":  true,
						"auth_url": "",
						"api_url":  "https://overwritten-api.com/user",
						"team_ids": "",
					},
					Source: models.DB,
				}
				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedConfigs = map[string]map[string]any{
					"github": {
						"auth_url":  "https://github.com/login/oauth/authorize",
						"token_url": "https://github.com/login/oauth/access_token",
						"api_url":   "https://api.github.com/user",
						"team_ids":  "10,11,12",
					},
				}
			},
			want: &models.SSOSettings{
				Provider: "github",
				Settings: map[string]any{
					"enabled":   true,
					"auth_url":  "https://github.com/login/oauth/authorize",
					"token_url": "https://github.com/login/oauth/access_token",
					"api_url":   "https://overwritten-api.com/user",
					"team_ids":  "",
				},
				Source: models.DB,
			},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		// create a local copy of "tc" to allow concurrent access within tests to the different items of testCases,
		// otherwise it would be like a moving pointer while tests run in parallel
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := setupTestEnv(t, false, false, nil)
			if tc.setup != nil {
				tc.setup(env)
			}

			actual, err := env.service.GetForProvider(context.Background(), "github")

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, actual)

			env.secrets.AssertExpectations(t)
		})
	}
}

func TestService_GetForProviderWithRedactedSecrets(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		setup   func(env testEnv)
		want    *models.SSOSettings
		wantErr bool
	}{
		{
			name: "should return successfully and redact secrets",
			setup: func(env testEnv) {
				env.store.ExpectedSSOSetting = &models.SSOSettings{
					Provider: "github",
					Settings: map[string]any{
						"enabled":       true,
						"secret":        base64.RawStdEncoding.EncodeToString([]byte("secret")),
						"client_secret": base64.RawStdEncoding.EncodeToString([]byte("client_secret")),
						"client_id":     "client_id",
					},
					Source: models.DB,
				}
				env.secrets.On("Decrypt", mock.Anything, []byte("client_secret"), mock.Anything).Return([]byte("decrypted-client-secret"), nil).Once()
				env.secrets.On("Decrypt", mock.Anything, []byte("secret"), mock.Anything).Return([]byte("decrypted-secret"), nil).Once()
			},
			want: &models.SSOSettings{
				Provider: "github",
				Settings: map[string]any{
					"enabled":       true,
					"secret":        "*********",
					"client_secret": "*********",
					"client_id":     "client_id",
				},
			},
			wantErr: false,
		},
		{
			name:    "should return error if store returns an error different than not found",
			setup:   func(env testEnv) { env.store.ExpectedError = fmt.Errorf("error") },
			want:    nil,
			wantErr: true,
		},
		{
			name: "should fallback to strategy if store returns not found",
			setup: func(env testEnv) {
				env.store.ExpectedError = ssosettings.ErrNotFound
				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedConfigs = map[string]map[string]any{
					"github": {
						"enabled": true,
					},
				}
			},
			want: &models.SSOSettings{
				Provider: "github",
				Settings: map[string]any{"enabled": true},
				Source:   models.System,
			},
			wantErr: false,
		},
		{
			name: "should return error if the fallback strategy was not found",
			setup: func(env testEnv) {
				env.store.ExpectedError = ssosettings.ErrNotFound
				env.fallbackStrategy.ExpectedIsMatch = false
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "should return error if fallback strategy returns error",
			setup: func(env testEnv) {
				env.store.ExpectedError = ssosettings.ErrNotFound
				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedError = fmt.Errorf("error")
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		// create a local copy of "tc" to allow concurrent access within tests to the different items of testCases,
		// otherwise it would be like a moving pointer while tests run in parallel
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := setupTestEnv(t, false, false, nil)
			if tc.setup != nil {
				tc.setup(env)
			}

			actual, err := env.service.GetForProviderWithRedactedSecrets(context.Background(), "github")

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, actual)
		})
	}
}

func TestService_List(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		setup   func(env testEnv)
		want    []*models.SSOSettings
		wantErr bool
	}{
		{
			name: "should return all oauth providers successfully without saml",
			setup: func(env testEnv) {
				env.store.ExpectedSSOSettings = []*models.SSOSettings{
					{
						Provider: "github",
						Settings: map[string]any{
							"enabled":       true,
							"client_secret": base64.RawStdEncoding.EncodeToString([]byte("client_secret")),
						},
						Source: models.DB,
					},
					{
						Provider: "okta",
						Settings: map[string]any{
							"enabled":      false,
							"other_secret": base64.RawStdEncoding.EncodeToString([]byte("other_secret")),
						},
						Source: models.DB,
					},
				}
				env.secrets.On("Decrypt", mock.Anything, []byte("client_secret"), mock.Anything).Return([]byte("decrypted-client-secret"), nil).Once()
				env.secrets.On("Decrypt", mock.Anything, []byte("other_secret"), mock.Anything).Return([]byte("decrypted-other-secret"), nil).Once()

				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedConfigs = map[string]map[string]any{
					"github": {
						"enabled":       false,
						"client_id":     "client_id",
						"client_secret": "secret1",
						"token_url":     "token_url",
					},
					"okta": {
						"enabled":       false,
						"client_id":     "client_id",
						"client_secret": "coming-from-system",
						"other_secret":  "secret2",
						"token_url":     "token_url",
					},
					"gitlab": {
						"enabled": false,
					},
					"generic_oauth": {
						"enabled": false,
					},
					"google": {
						"enabled": false,
					},
					"azuread": {
						"enabled": false,
					},
					"grafana_com": {
						"enabled": false,
					},
				}
			},
			want: []*models.SSOSettings{
				{
					Provider: "github",
					Settings: map[string]any{
						"enabled":       true,
						"client_id":     "client_id",
						"client_secret": "decrypted-client-secret", // client_secret is coming from the database, must be decrypted first
						"token_url":     "token_url",
					},
					Source: models.DB,
				},
				{
					Provider: "okta",
					Settings: map[string]any{
						"enabled":       false,
						"client_id":     "client_id",
						"client_secret": "coming-from-system", // client_secret is coming from the system, must not be decrypted
						"other_secret":  "decrypted-other-secret",
						"token_url":     "token_url",
					},
					Source: models.DB,
				},
				{
					Provider: "gitlab",
					Settings: map[string]any{"enabled": false},
					Source:   models.System,
				},
				{
					Provider: "generic_oauth",
					Settings: map[string]any{"enabled": false},
					Source:   models.System,
				},
				{
					Provider: "google",
					Settings: map[string]any{"enabled": false},
					Source:   models.System,
				},
				{
					Provider: "azuread",
					Settings: map[string]any{"enabled": false},
					Source:   models.System,
				},
				{
					Provider: "grafana_com",
					Settings: map[string]any{"enabled": false},
					Source:   models.System,
				},
			},
			wantErr: false,
		},
		{
			name: "should return error if any of the fallback strategies was not found",
			setup: func(env testEnv) {
				env.store.ExpectedSSOSettings = []*models.SSOSettings{}
				env.fallbackStrategy.ExpectedIsMatch = false
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tc := range testCases {
		// create a local copy of "tc" to allow concurrent access within tests to the different items of testCases,
		// otherwise it would be like a moving pointer while tests run in parallel
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := setupTestEnv(t, false, false, nil)
			if tc.setup != nil {
				tc.setup(env)
			}

			actual, err := env.service.List(context.Background())

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.ElementsMatch(t, tc.want, actual)
		})
	}
}

func TestService_ListWithRedactedSecrets(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		setup   func(env testEnv)
		want    []*models.SSOSettings
		wantErr bool
	}{
		{
			name: "should return successfully",
			setup: func(env testEnv) {
				env.store.ExpectedSSOSettings = []*models.SSOSettings{
					{
						Provider: "github",
						Settings: map[string]any{
							"enabled":       true,
							"client_secret": base64.RawStdEncoding.EncodeToString([]byte("client_secret")),
							"client_id":     "client_id",
						},
						Source: models.DB,
					},
					{
						Provider: "okta",
						Settings: map[string]any{
							"enabled":      false,
							"other_secret": base64.RawStdEncoding.EncodeToString([]byte("other_secret")),
							"client_id":    "client_id",
						},
						Source: models.DB,
					},
				}
				env.secrets.On("Decrypt", mock.Anything, []byte("client_secret"), mock.Anything).Return([]byte("decrypted-client-secret"), nil).Once()
				env.secrets.On("Decrypt", mock.Anything, []byte("other_secret"), mock.Anything).Return([]byte("decrypted-other-secret"), nil).Once()

				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedConfigs = map[string]map[string]any{
					"github": {
						"enabled":       true,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"okta": {
						"enabled":       true,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"gitlab": {
						"enabled":       true,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"generic_oauth": {
						"enabled":       true,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"google": {
						"enabled":       true,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"azuread": {
						"enabled":       true,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"grafana_com": {
						"enabled":       true,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
				}
			},
			want: []*models.SSOSettings{
				{
					Provider: "github",
					Settings: map[string]any{
						"enabled":       true,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
					},
					Source: models.DB,
				},
				{
					Provider: "okta",
					Settings: map[string]any{
						"enabled":       false,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
						"other_secret":  "*********",
					},
					Source: models.DB,
				},
				{
					Provider: "gitlab",
					Settings: map[string]any{
						"enabled":       true,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
					},
					Source: models.System,
				},
				{
					Provider: "generic_oauth",
					Settings: map[string]any{
						"enabled":       true,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
					},
					Source: models.System,
				},
				{
					Provider: "google",
					Settings: map[string]any{
						"enabled":       true,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
					},
					Source: models.System,
				},
				{
					Provider: "azuread",
					Settings: map[string]any{
						"enabled":       true,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
					},
					Source: models.System,
				},
			},
			wantErr: false,
		},
		{
			name:    "should return error if store returns an error",
			setup:   func(env testEnv) { env.store.ExpectedError = fmt.Errorf("error") },
			want:    nil,
			wantErr: true,
		},
		{
			name: "should use the fallback strategy if store returns empty list",
			setup: func(env testEnv) {
				env.store.ExpectedSSOSettings = []*models.SSOSettings{}
				env.fallbackStrategy.ExpectedIsMatch = true
				env.fallbackStrategy.ExpectedConfigs = map[string]map[string]any{
					"github": {
						"enabled":       false,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"okta": {
						"enabled":       false,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"gitlab": {
						"enabled":       false,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"generic_oauth": {
						"enabled":       false,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"google": {
						"enabled":       false,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"azuread": {
						"enabled":       false,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
					"grafana_com": {
						"enabled":       false,
						"secret":        "secret",
						"client_secret": "client_secret",
						"client_id":     "client_id",
					},
				}
			},
			want: []*models.SSOSettings{
				{
					Provider: "github",
					Settings: map[string]any{
						"enabled":       false,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
					},
					Source: models.System,
				},
				{
					Provider: "okta",
					Settings: map[string]any{
						"enabled":       false,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
					},
					Source: models.System,
				},
				{
					Provider: "gitlab",
					Settings: map[string]any{
						"enabled":       false,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
					},
					Source: models.System,
				},
				{
					Provider: "generic_oauth",
					Settings: map[string]any{
						"enabled":       false,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
					},
					Source: models.System,
				},
				{
					Provider: "google",
					Settings: map[string]any{
						"enabled":       false,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
					},
					Source: models.System,
				},
				{
					Provider: "azuread",
					Settings: map[string]any{
						"enabled":       false,
						"secret":        "*********",
						"client_secret": "*********",
						"client_id":     "client_id",
					},
					Source: models.System,
				},
			},
			wantErr: false,
		},
		{
			name: "should return error if any of the fallback strategies was not found",
			setup: func(env testEnv) {
				env.store.ExpectedSSOSettings = []*models.SSOSettings{}
				env.fallbackStrategy.ExpectedIsMatch = false
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tc := range testCases {
		// create a local copy of "tc" to allow concurrent access within tests to the different items of testCases,
		// otherwise it would be like a moving pointer while tests run in parallel
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := setupTestEnv(t, false, false, nil)
			if tc.setup != nil {
				tc.setup(env)
			}

			actual, err := env.service.ListWithRedactedSecrets(context.Background())

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.ElementsMatch(t, tc.want, actual)
		})
	}
}

func TestService_Upsert(t *testing.T) {
	t.Parallel()

	t.Run("successfully upsert SSO settings", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := social.AzureADProviderName
		settings := models.SSOSettings{
			Provider: provider,
			Settings: map[string]any{
				"client_id":     "client-id",
				"client_secret": "client-secret",
				"enabled":       true,
			},
			IsDeleted: false,
		}
		var wg sync.WaitGroup
		wg.Add(1)

		reloadable := ssosettingstests.NewMockReloadable(t)
		reloadable.On("Validate", mock.Anything, settings, mock.Anything).Return(nil)
		reloadable.On("Reload", mock.Anything, mock.MatchedBy(func(settings models.SSOSettings) bool {
			defer wg.Done()
			return settings.Provider == provider &&
				settings.ID == "someid" &&
				maps.Equal(settings.Settings, map[string]any{
					"client_id":     "client-id",
					"client_secret": "client-secret",
					"enabled":       true,
				})
		})).Return(nil).Once()
		env.reloadables[provider] = reloadable
		env.secrets.On("Encrypt", mock.Anything, []byte(settings.Settings["client_secret"].(string)), mock.Anything).Return([]byte("encrypted-client-secret"), nil).Once()
		env.secrets.On("Decrypt", mock.Anything, []byte("encrypted-current-client-secret"), mock.Anything).Return([]byte("current-client-secret"), nil).Once()

		env.store.UpsertFn = func(ctx context.Context, settings *models.SSOSettings) error {
			currentTime := time.Now()
			settings.ID = "someid"
			settings.Created = currentTime
			settings.Updated = currentTime

			env.store.ActualSSOSettings = *settings
			return nil
		}

		env.store.GetFn = func(ctx context.Context, provider string) (*models.SSOSettings, error) {
			return &models.SSOSettings{
				ID:       "someid",
				Provider: provider,
				Settings: map[string]any{
					"client_secret": base64.RawStdEncoding.EncodeToString([]byte("encrypted-current-client-secret")),
				},
			}, nil
		}
		err := env.service.Upsert(context.Background(), &settings, &user.SignedInUser{})
		require.NoError(t, err)

		// Wait for the goroutine first to assert the Reload call
		wg.Wait()

		settings.Settings["client_secret"] = base64.RawStdEncoding.EncodeToString([]byte("encrypted-client-secret"))
		require.EqualValues(t, settings, env.store.ActualSSOSettings)
	})

	t.Run("returns error if provider is not configurable", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := social.GrafanaComProviderName
		settings := &models.SSOSettings{
			Provider: provider,
			Settings: map[string]any{
				"client_id":     "client-id",
				"client_secret": "client-secret",
				"enabled":       true,
			},
			IsDeleted: false,
		}

		reloadable := ssosettingstests.NewMockReloadable(t)
		env.reloadables[provider] = reloadable

		err := env.service.Upsert(context.Background(), settings, &user.SignedInUser{})
		require.Error(t, err)
	})

	t.Run("returns error if provider was not found in reloadables", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := social.AzureADProviderName
		settings := &models.SSOSettings{
			Provider: provider,
			Settings: map[string]any{
				"client_id":     "client-id",
				"client_secret": "client-secret",
				"enabled":       true,
			},
			IsDeleted: false,
		}

		reloadable := ssosettingstests.NewMockReloadable(t)
		// the reloadable is available for other provider
		env.reloadables["github"] = reloadable

		err := env.service.Upsert(context.Background(), settings, &user.SignedInUser{})
		require.Error(t, err)
	})

	t.Run("returns error if validation fails", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := social.AzureADProviderName
		settings := models.SSOSettings{
			Provider: provider,
			Settings: map[string]any{
				"client_id":     "client-id",
				"client_secret": "client-secret",
				"enabled":       true,
			},
			IsDeleted: false,
		}

		reloadable := ssosettingstests.NewMockReloadable(t)
		reloadable.On("Validate", mock.Anything, settings, mock.Anything).Return(errors.New("validation failed"))
		env.reloadables[provider] = reloadable

		err := env.service.Upsert(context.Background(), &settings, &user.SignedInUser{})
		require.Error(t, err)
	})

	t.Run("returns error if a fallback strategy is not available for the provider", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		settings := &models.SSOSettings{
			Provider: social.AzureADProviderName,
			Settings: map[string]any{
				"client_id":     "client-id",
				"client_secret": "client-secret",
				"enabled":       true,
			},
			IsDeleted: false,
		}

		env.fallbackStrategy.ExpectedIsMatch = false

		err := env.service.Upsert(context.Background(), settings, &user.SignedInUser{})
		require.Error(t, err)
	})

	t.Run("returns error if secrets encryption failed", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := social.OktaProviderName
		settings := models.SSOSettings{
			Provider: provider,
			Settings: map[string]any{
				"client_id":     "client-id",
				"client_secret": "client-secret",
				"enabled":       true,
			},
			IsDeleted: false,
		}

		reloadable := ssosettingstests.NewMockReloadable(t)
		reloadable.On("Validate", mock.Anything, settings, mock.Anything).Return(nil)
		env.reloadables[provider] = reloadable
		env.secrets.On("Encrypt", mock.Anything, []byte(settings.Settings["client_secret"].(string)), mock.Anything).Return(nil, errors.New("encryption failed")).Once()

		err := env.service.Upsert(context.Background(), &settings, &user.SignedInUser{})
		require.Error(t, err)
	})

	t.Run("should not update the current secret if the secret has not been updated", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := social.AzureADProviderName
		settings := models.SSOSettings{
			Provider: provider,
			Settings: map[string]any{
				"client_id":     "client-id",
				"client_secret": setting.RedactedPassword,
				"enabled":       true,
			},
			IsDeleted: false,
		}

		env.store.ExpectedSSOSetting = &models.SSOSettings{
			Provider: provider,
			Settings: map[string]any{
				"client_secret": base64.RawStdEncoding.EncodeToString([]byte("current-client-secret")),
			},
		}

		reloadable := ssosettingstests.NewMockReloadable(t)
		reloadable.On("Validate", mock.Anything, settings, mock.Anything).Return(nil)
		reloadable.On("Reload", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.reloadables[provider] = reloadable
		env.secrets.On("Decrypt", mock.Anything, []byte("current-client-secret"), mock.Anything).Return([]byte("encrypted-client-secret"), nil).Once()
		env.secrets.On("Encrypt", mock.Anything, []byte("encrypted-client-secret"), mock.Anything).Return([]byte("current-client-secret"), nil).Once()

		err := env.service.Upsert(context.Background(), &settings, &user.SignedInUser{})
		require.NoError(t, err)

		settings.Settings["client_secret"] = base64.RawStdEncoding.EncodeToString([]byte("current-client-secret"))
		require.EqualValues(t, settings, env.store.ActualSSOSettings)
	})

	t.Run("returns error if store failed to upsert settings", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := social.AzureADProviderName
		settings := models.SSOSettings{
			Provider: provider,
			Settings: map[string]any{
				"client_id":     "client-id",
				"client_secret": "client-secret",
				"enabled":       true,
			},
			IsDeleted: false,
		}

		reloadable := ssosettingstests.NewMockReloadable(t)
		reloadable.On("Validate", mock.Anything, settings, mock.Anything).Return(nil)
		env.reloadables[provider] = reloadable
		env.secrets.On("Encrypt", mock.Anything, []byte(settings.Settings["client_secret"].(string)), mock.Anything).Return([]byte("encrypted-client-secret"), nil).Once()
		env.store.GetFn = func(ctx context.Context, provider string) (*models.SSOSettings, error) {
			return &models.SSOSettings{}, nil
		}

		env.store.UpsertFn = func(ctx context.Context, settings *models.SSOSettings) error {
			return errors.New("failed to upsert settings")
		}

		err := env.service.Upsert(context.Background(), &settings, &user.SignedInUser{})
		require.Error(t, err)
	})

	t.Run("successfully upsert SSO settings if reload fails", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := social.AzureADProviderName
		settings := models.SSOSettings{
			Provider: provider,
			Settings: map[string]any{
				"client_id":     "client-id",
				"client_secret": "client-secret",
				"enabled":       true,
			},
			IsDeleted: false,
		}

		reloadable := ssosettingstests.NewMockReloadable(t)
		reloadable.On("Validate", mock.Anything, settings, mock.Anything).Return(nil)
		reloadable.On("Reload", mock.Anything, mock.Anything).Return(errors.New("failed reloading new settings")).Maybe()
		env.reloadables[provider] = reloadable
		env.secrets.On("Encrypt", mock.Anything, []byte(settings.Settings["client_secret"].(string)), mock.Anything).Return([]byte("encrypted-client-secret"), nil).Once()

		err := env.service.Upsert(context.Background(), &settings, &user.SignedInUser{})
		require.NoError(t, err)

		settings.Settings["client_secret"] = base64.RawStdEncoding.EncodeToString([]byte("encrypted-client-secret"))
		require.EqualValues(t, settings, env.store.ActualSSOSettings)
	})
}

func TestService_Delete(t *testing.T) {
	t.Parallel()

	t.Run("successfully delete SSO settings", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		var wg sync.WaitGroup
		wg.Add(1)

		provider := social.AzureADProviderName
		reloadable := ssosettingstests.NewMockReloadable(t)
		env.reloadables[provider] = reloadable

		env.fallbackStrategy.ExpectedConfigs = map[string]map[string]any{
			provider: {
				"client_id":     "client-id",
				"client_secret": "client-secret",
				"enabled":       true,
			},
		}

		reloadable.On("Reload", mock.Anything, mock.MatchedBy(func(settings models.SSOSettings) bool {
			wg.Done()
			return settings.Provider == provider &&
				settings.ID == "" &&
				maps.Equal(settings.Settings, map[string]any{
					"client_id":     "client-id",
					"client_secret": "client-secret",
					"enabled":       true,
				})
		})).Return(nil).Once()

		err := env.service.Delete(context.Background(), provider)
		require.NoError(t, err)

		// wait for the goroutine first to assert the Reload call
		wg.Wait()
	})

	t.Run("return error if SSO setting was not found for the specified provider", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := social.AzureADProviderName
		reloadable := ssosettingstests.NewMockReloadable(t)
		env.reloadables[provider] = reloadable
		env.store.ExpectedError = ssosettings.ErrNotFound

		err := env.service.Delete(context.Background(), provider)
		require.Error(t, err)

		require.ErrorIs(t, err, ssosettings.ErrNotFound)
	})

	t.Run("should not delete the SSO settings if the provider is not configurable", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)
		env.cfg.SSOSettingsConfigurableProviders = map[string]bool{social.AzureADProviderName: true}

		provider := social.GrafanaComProviderName
		env.store.ExpectedError = nil

		err := env.service.Delete(context.Background(), provider)
		require.ErrorIs(t, err, ssosettings.ErrNotConfigurable)
	})

	t.Run("return error when store fails to delete the SSO settings for the specified provider", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := social.AzureADProviderName
		env.store.ExpectedError = errors.New("delete sso settings failed")

		err := env.service.Delete(context.Background(), provider)
		require.Error(t, err)
		require.NotErrorIs(t, err, ssosettings.ErrNotFound)
	})

	t.Run("return successfully when the deletion was successful but reloading the settings fail", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := social.AzureADProviderName
		reloadable := ssosettingstests.NewMockReloadable(t)
		env.reloadables[provider] = reloadable

		env.store.GetFn = func(ctx context.Context, provider string) (*models.SSOSettings, error) {
			return nil, errors.New("failed to get sso settings")
		}

		err := env.service.Delete(context.Background(), provider)

		require.NoError(t, err)
	})
}

func TestService_DoReload(t *testing.T) {
	t.Parallel()

	t.Run("successfully reload settings", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		settingsList := []*models.SSOSettings{
			{
				Provider: "github",
				Settings: map[string]any{
					"enabled":   true,
					"client_id": "github_client_id",
				},
			},
			{
				Provider: "google",
				Settings: map[string]any{
					"enabled":   true,
					"client_id": "google_client_id",
				},
			},
			{
				Provider: "azuread",
				Settings: map[string]any{
					"enabled":   true,
					"client_id": "azuread_client_id",
				},
			},
		}
		env.store.ExpectedSSOSettings = settingsList

		reloadable := ssosettingstests.NewMockReloadable(t)

		for _, settings := range settingsList {
			reloadable.On("Reload", mock.Anything, *settings).Return(nil).Once()
			env.reloadables[settings.Provider] = reloadable
		}

		env.service.doReload(context.Background())
	})

	t.Run("successfully reload settings when some providers have empty settings", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		settingsList := []*models.SSOSettings{
			{
				Provider: "azuread",
				Settings: map[string]any{
					"enabled":   true,
					"client_id": "azuread_client_id",
				},
			},
			{
				Provider: "google",
				Settings: map[string]any{},
			},
		}
		env.store.ExpectedSSOSettings = settingsList

		reloadable := ssosettingstests.NewMockReloadable(t)
		reloadable.On("Reload", mock.Anything, *settingsList[0]).Return(nil).Once()
		env.reloadables["azuread"] = reloadable

		// registers a provider with empty settings
		env.reloadables["github"] = nil

		env.service.doReload(context.Background())
	})

	t.Run("failed fetching the SSO settings", func(t *testing.T) {
		t.Parallel()

		env := setupTestEnv(t, false, false, nil)

		provider := "github"

		env.store.ExpectedError = errors.New("failed fetching the settings")

		reloadable := ssosettingstests.NewMockReloadable(t)
		env.reloadables[provider] = reloadable

		env.service.doReload(context.Background())
	})
}

func TestService_decryptSecrets(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		setup    func(env testEnv)
		settings map[string]any
		want     map[string]any
		wantErr  bool
	}{
		{
			name: "should decrypt secrets successfully",
			setup: func(env testEnv) {
				env.secrets.On("Decrypt", mock.Anything, []byte("client_secret"), mock.Anything).Return([]byte("decrypted-client-secret"), nil).Once()
				env.secrets.On("Decrypt", mock.Anything, []byte("other_secret"), mock.Anything).Return([]byte("decrypted-other-secret"), nil).Once()
				env.secrets.On("Decrypt", mock.Anything, []byte("private_key"), mock.Anything).Return([]byte("decrypted-private-key"), nil).Once()
				env.secrets.On("Decrypt", mock.Anything, []byte("certificate"), mock.Anything).Return([]byte("decrypted-certificate"), nil).Once()
			},
			settings: map[string]any{
				"enabled":       true,
				"client_secret": base64.RawStdEncoding.EncodeToString([]byte("client_secret")),
				"other_secret":  base64.RawStdEncoding.EncodeToString([]byte("other_secret")),
				"private_key":   base64.RawStdEncoding.EncodeToString([]byte("private_key")),
				"certificate":   base64.RawStdEncoding.EncodeToString([]byte("certificate")),
			},
			want: map[string]any{
				"enabled":       true,
				"client_secret": "decrypted-client-secret",
				"other_secret":  "decrypted-other-secret",
				"private_key":   "decrypted-private-key",
				"certificate":   "decrypted-certificate",
			},
		},
		{
			name: "should not decrypt when a secret is empty",
			setup: func(env testEnv) {
				env.secrets.On("Decrypt", mock.Anything, []byte("other_secret"), mock.Anything).Return([]byte("decrypted-other-secret"), nil).Once()
			},
			settings: map[string]any{
				"enabled":       true,
				"client_secret": "",
				"other_secret":  base64.RawStdEncoding.EncodeToString([]byte("other_secret")),
			},
			want: map[string]any{
				"enabled":       true,
				"client_secret": "",
				"other_secret":  "decrypted-other-secret",
			},
		},
		{
			name: "should return an error if data is not a string",
			settings: map[string]any{
				"enabled":       true,
				"client_secret": 2,
				"other_secret":  2,
			},
			wantErr: true,
		},
		{
			name: "should return an error if data is not a valid base64 string",
			settings: map[string]any{
				"enabled":       true,
				"client_secret": "client_secret",
				"other_secret":  "other_secret",
			},
			wantErr: true,
		},
		{
			name: "should return an error if decryption fails",
			setup: func(env testEnv) {
				env.secrets.On("Decrypt", mock.Anything, []byte("client_secret"), mock.Anything).Return(nil, errors.New("decryption failed")).Once()
			},
			settings: map[string]any{
				"enabled":       true,
				"client_secret": base64.RawStdEncoding.EncodeToString([]byte("client_secret")),
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		// create a local copy of "tc" to allow concurrent access within tests to the different items of testCases,
		// otherwise it would be like a moving pointer while tests run in parallel
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := setupTestEnv(t, false, false, nil)

			if tc.setup != nil {
				tc.setup(env)
			}

			actual, err := env.service.decryptSecrets(context.Background(), tc.settings)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, actual)

			env.secrets.AssertExpectations(t)
		})
	}
}

func Test_ProviderService(t *testing.T) {
	tests := []struct {
		name                  string
		isLicenseEnabled      bool
		configurableProviders map[string]bool
		expectedProvidersList []string
		strategiesLength      int
	}{
		{
			name:             "should return all OAuth providers but not saml because the licensing feature is not enabled",
			isLicenseEnabled: false,
			expectedProvidersList: []string{
				"github",
				"gitlab",
				"google",
				"generic_oauth",
				"grafana_com",
				"azuread",
				"okta",
			},
			strategiesLength: 1,
		},
		{
			name:             "should return all fallback strategies and it should return all OAuth providers but not saml because the licensing feature is enabled but the configurable provider is not setup",
			isLicenseEnabled: true,
			expectedProvidersList: []string{
				"github",
				"gitlab",
				"google",
				"generic_oauth",
				"grafana_com",
				"azuread",
				"okta",
			},
			strategiesLength: 2,
		},
		{
			name:                  "should return all fallback strategies and it should return all OAuth providers and saml because the licensing feature is enabled and the provider is setup",
			isLicenseEnabled:      true,
			configurableProviders: map[string]bool{"saml": true},
			expectedProvidersList: []string{
				"github",
				"gitlab",
				"google",
				"generic_oauth",
				"grafana_com",
				"azuread",
				"okta",
				"saml",
			},
			strategiesLength: 2,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := setupTestEnv(t, tc.isLicenseEnabled, true, tc.configurableProviders)

			require.Equal(t, tc.expectedProvidersList, env.service.providersList)
			require.Equal(t, tc.strategiesLength, len(env.service.fbStrategies))
		})
	}
}

func setupTestEnv(t *testing.T, isLicensingEnabled, keepFallbackStratergies bool, extraConfigurableProviders map[string]bool) testEnv {
	t.Helper()

	store := ssosettingstests.NewFakeStore()
	fallbackStrategy := ssosettingstests.NewFakeFallbackStrategy()
	secrets := secretsFakes.NewMockService(t)
	accessControl := acimpl.ProvideAccessControl(setting.NewCfg())
	reloadables := make(map[string]ssosettings.Reloadable)

	fallbackStrategy.ExpectedIsMatch = true

	iniFile, _ := ini.Load([]byte(""))

	configurableProviders := map[string]bool{
		"github":        true,
		"okta":          true,
		"azuread":       true,
		"google":        true,
		"generic_oauth": true,
		"gitlab":        true,
	}

	for k, v := range extraConfigurableProviders {
		configurableProviders[k] = v
	}

	cfg := &setting.Cfg{
		SSOSettingsConfigurableProviders: configurableProviders,
		Raw:                              iniFile,
	}

	licensing := licensingtest.NewFakeLicensing()
	licensing.On("FeatureEnabled", "saml").Return(isLicensingEnabled)

	svc := ProvideService(
		cfg,
		&dbtest.FakeDB{},
		accessControl,
		routing.NewRouteRegister(),
		featuremgmt.WithManager(nil),
		secretsFakes.NewMockService(t),
		&usagestats.UsageStatsMock{},
		prometheus.NewRegistry(),
		&setting.OSSImpl{Cfg: cfg},
		licensing,
	)

	// overriding values for exposed fields
	svc.store = store
	if !keepFallbackStratergies {
		svc.fbStrategies = []ssosettings.FallbackStrategy{
			fallbackStrategy,
		}
	}
	svc.secrets = secrets
	svc.reloadables = reloadables

	return testEnv{
		cfg:              cfg,
		service:          svc,
		store:            store,
		ac:               accessControl,
		fallbackStrategy: fallbackStrategy,
		secrets:          secrets,
		reloadables:      reloadables,
	}
}

type testEnv struct {
	cfg              *setting.Cfg
	service          *Service
	store            *ssosettingstests.FakeStore
	ac               accesscontrol.AccessControl
	fallbackStrategy *ssosettingstests.FakeFallbackStrategy
	secrets          *secretsFakes.MockService
	reloadables      map[string]ssosettings.Reloadable
}
