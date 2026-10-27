package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ijaidev/gojent/codjent/internal/app"
	"github.com/ijaidev/gojent/codjent/internal/provider"
)

// ListProviderConfigs implements catalog.Store.
func (s *Store) ListProviderConfigs(ctx context.Context) ([]*provider.Config, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, format, base_url, default_model, api_key, created_at, updated_at
		FROM providers ORDER BY created_at ASC, rowid ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list providers: %w", err)
	}

	configs := make([]*provider.Config, 0)
	for rows.Next() {
		config, err := scanProvider(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("sqlite: scan provider: %w", err)
		}
		configs = append(configs, config)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("sqlite: list providers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("sqlite: list providers: %w", err)
	}

	// The connection pool has a single connection, so model rows are loaded
	// after the provider cursor is closed.
	for _, config := range configs {
		models, err := s.listProviderModels(ctx, config.ID)
		if err != nil {
			return nil, err
		}
		config.Models = models
	}
	return configs, nil
}

// GetProviderConfig implements catalog.Store.
func (s *Store) GetProviderConfig(ctx context.Context, id string) (*provider.Config, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, format, base_url, default_model, api_key, created_at, updated_at
		FROM providers WHERE id = ?`, id)
	config, err := scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, provider.ErrConfigNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get provider: %w", err)
	}
	models, err := s.listProviderModels(ctx, id)
	if err != nil {
		return nil, err
	}
	config.Models = models
	return config, nil
}

// CreateProviderConfig implements catalog.Store.
func (s *Store) CreateProviderConfig(ctx context.Context, config *provider.Config) error {
	if config == nil {
		return fmt.Errorf("%w: provider configuration is nil", app.ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin create provider: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO providers (id, name, format, base_url, default_model, api_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		config.ID, config.Name, string(config.Format), config.BaseURL, config.DefaultModel,
		config.APIKey, formatTime(config.CreatedAt), formatTime(config.UpdatedAt),
	); err != nil {
		return fmt.Errorf("sqlite: create provider: %w", err)
	}
	if err := insertProviderModels(ctx, tx, config); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit create provider: %w", err)
	}
	return nil
}

// UpdateProviderConfig implements catalog.Store.
func (s *Store) UpdateProviderConfig(ctx context.Context, config *provider.Config) error {
	if config == nil {
		return fmt.Errorf("%w: provider configuration is nil", app.ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin update provider: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		UPDATE providers
		SET name = ?, format = ?, base_url = ?, default_model = ?, api_key = ?, updated_at = ?
		WHERE id = ?`,
		config.Name, string(config.Format), config.BaseURL, config.DefaultModel,
		config.APIKey, formatTime(config.UpdatedAt), config.ID,
	)
	if err != nil {
		return fmt.Errorf("sqlite: update provider: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: update provider: %w", err)
	}
	if affected == 0 {
		return provider.ErrConfigNotFound
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM provider_models WHERE provider_id = ?`, config.ID); err != nil {
		return fmt.Errorf("sqlite: replace provider models: %w", err)
	}
	if err := insertProviderModels(ctx, tx, config); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit update provider: %w", err)
	}
	return nil
}

// DeleteProviderConfig implements catalog.Store. The thread-reference check and
// delete share one transaction so a concurrent thread cannot start using a
// provider while it is being removed.
func (s *Store) DeleteProviderConfig(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin delete provider: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM threads WHERE provider = ?`, id).Scan(&count); err != nil {
		return fmt.Errorf("sqlite: count provider threads: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("%w: %d thread(s) still use it", provider.ErrConfigInUse, count)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: delete provider: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: delete provider: %w", err)
	}
	if affected == 0 {
		return provider.ErrConfigNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit delete provider: %w", err)
	}
	return nil
}

// CountThreadsByProvider counts threads pinned to one provider. It is used to
// preserve the legacy Google provider on upgrade.
func (s *Store) CountThreadsByProvider(ctx context.Context, providerID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM threads WHERE provider = ?`, providerID).Scan(&count); err != nil {
		return 0, fmt.Errorf("sqlite: count provider threads: %w", err)
	}
	return count, nil
}

func (s *Store) listProviderModels(ctx context.Context, providerID string) ([]provider.ModelInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT model_id, name FROM provider_models
		WHERE provider_id = ? ORDER BY position ASC`, providerID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list provider models: %w", err)
	}
	defer func() { _ = rows.Close() }()

	models := make([]provider.ModelInfo, 0)
	for rows.Next() {
		var model provider.ModelInfo
		if err := rows.Scan(&model.ID, &model.Name); err != nil {
			return nil, fmt.Errorf("sqlite: scan provider model: %w", err)
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list provider models: %w", err)
	}
	return models, nil
}

func insertProviderModels(ctx context.Context, tx *sql.Tx, config *provider.Config) error {
	for position, model := range config.Models {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO provider_models (provider_id, position, model_id, name)
			VALUES (?, ?, ?, ?)`,
			config.ID, position, model.ID, model.Name,
		); err != nil {
			return fmt.Errorf("sqlite: create provider model: %w", err)
		}
	}
	return nil
}

func scanProvider(row scanner) (*provider.Config, error) {
	var (
		config    provider.Config
		format    string
		createdAt string
		updatedAt string
	)
	if err := row.Scan(
		&config.ID, &config.Name, &format, &config.BaseURL, &config.DefaultModel,
		&config.APIKey, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	config.Format = provider.Format(format)
	config.CreatedAt = parseTime(createdAt)
	config.UpdatedAt = parseTime(updatedAt)
	return &config, nil
}
