package certificate

import (
	"database/sql"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store manages trusted certificates in the database and session memory
type Store struct {
	db      *sql.DB
	mu      sync.RWMutex
	session map[string]bool // fingerprint -> trusted (session only)
}

// NewStore creates a new certificate trust store
func NewStore(db *sql.DB) *Store {
	return &Store{
		db:      db,
		session: make(map[string]bool),
	}
}

// IsTrusted checks if a certificate fingerprint is trusted (DB or session)
func (s *Store) IsTrusted(fingerprint string) bool {
	// Check session memory first (fast path)
	s.mu.RLock()
	if s.session[fingerprint] {
		s.mu.RUnlock()
		return true
	}
	s.mu.RUnlock()

	// Check database
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM trusted_certificates WHERE fingerprint = ?",
		fingerprint,
	).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// AcceptPermanently stores a certificate in the database
func (s *Store) AcceptPermanently(host string, info *CertificateInfo) error {
	id := uuid.New().String()
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO trusted_certificates (id, fingerprint, host, subject, issuer, not_before, not_after, accepted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, info.Fingerprint, host, info.Subject, info.Issuer, info.NotBefore, info.NotAfter, time.Now(),
	)
	return err
}

// AcceptSession stores a certificate fingerprint in session memory only
func (s *Store) AcceptSession(fingerprint string) {
	s.mu.Lock()
	s.session[fingerprint] = true
	s.mu.Unlock()
}

// GetByHosts returns permanently trusted certificates for the given hosts
func (s *Store) GetByHosts(hosts []string) ([]*CertificateInfo, error) {
	if len(hosts) == 0 {
		return nil, nil
	}

	// Build query with placeholders
	query := "SELECT fingerprint, host, subject, issuer, not_before, not_after FROM trusted_certificates WHERE host IN ("
	args := make([]interface{}, len(hosts))
	for i, h := range hosts {
		if i > 0 {
			query += ","
		}
		query += "?"
		args[i] = h
	}
	query += ") ORDER BY accepted_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var certs []*CertificateInfo
	for rows.Next() {
		var ci CertificateInfo
		var host string
		if err := rows.Scan(&ci.Fingerprint, &host, &ci.Subject, &ci.Issuer, &ci.NotBefore, &ci.NotAfter); err != nil {
			return nil, err
		}
		certs = append(certs, &ci)
	}
	return certs, rows.Err()
}

// Remove deletes a trusted certificate from the database by fingerprint
func (s *Store) Remove(fingerprint string) error {
	_, err := s.db.Exec("DELETE FROM trusted_certificates WHERE fingerprint = ?", fingerprint)
	if err != nil {
		return err
	}

	// Also remove from session
	s.mu.Lock()
	delete(s.session, fingerprint)
	s.mu.Unlock()

	return nil
}
