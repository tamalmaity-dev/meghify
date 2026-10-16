package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ProjectRepo persists projects and their environments.
type ProjectRepo struct{ db *sql.DB }

// CreateProject inserts a project.
func (r *ProjectRepo) CreateProject(ctx context.Context, p *Project) error {
	now := Text(time.Now().UTC())
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, now, now); err != nil {
		return fmt.Errorf("insert project: %w", err)
	}
	p.CreatedAt, p.UpdatedAt = Parse(now), Parse(now)
	return nil
}

func scanProject(scanner interface{ Scan(...any) error }) (*Project, error) {
	var p Project
	var createdAt, updatedAt string
	err := scanner.Scan(&p.ID, &p.Name, &p.Description, &createdAt, &updatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	p.CreatedAt, p.UpdatedAt = Parse(createdAt), Parse(updatedAt)
	return &p, nil
}

// ProjectByID loads a project.
func (r *ProjectRepo) ProjectByID(ctx context.Context, id string) (*Project, error) {
	p, err := scanProject(r.db.QueryRowContext(ctx,
		`SELECT id, name, description, created_at, updated_at FROM projects WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("project %s: %w", id, err)
	}
	return p, nil
}

// ListProjects returns every project.
func (r *ProjectRepo) ListProjects(ctx context.Context) ([]*Project, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, description, created_at, updated_at FROM projects ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateProject writes a project's name and description.
func (r *ProjectRepo) UpdateProject(ctx context.Context, id, name, description string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE projects SET name = ?, description = ?, updated_at = ? WHERE id = ?`,
		name, description, Text(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// DeleteProject removes a project and cascades to its environments.
func (r *ProjectRepo) DeleteProject(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// CreateEnvironment inserts an environment inside a project.
func (r *ProjectRepo) CreateEnvironment(ctx context.Context, e *Environment) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO environments (id, project_id, name, created_at) VALUES (?, ?, ?, ?)`,
		e.ID, e.ProjectID, e.Name, Text(time.Now().UTC())); err != nil {
		return fmt.Errorf("insert environment: %w", err)
	}
	return nil
}

// EnvironmentByID loads an environment.
func (r *ProjectRepo) EnvironmentByID(ctx context.Context, id string) (*Environment, error) {
	var e Environment
	var createdAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, project_id, name, created_at FROM environments WHERE id = ?`, id).
		Scan(&e.ID, &e.ProjectID, &e.Name, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	e.CreatedAt = Parse(createdAt)
	return &e, nil
}

// EnvironmentsByProject lists a project's environments.
func (r *ProjectRepo) EnvironmentsByProject(ctx context.Context, projectID string) ([]*Environment, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, project_id, name, created_at FROM environments WHERE project_id = ? ORDER BY created_at ASC`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Environment
	for rows.Next() {
		var e Environment
		var createdAt string
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Name, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = Parse(createdAt)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// DeleteEnvironment removes an environment.
func (r *ProjectRepo) DeleteEnvironment(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM environments WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}
