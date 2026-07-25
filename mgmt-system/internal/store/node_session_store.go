package store

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/gorm"
)

func (s *Store) UpdateNodeSessionConnected(serverID uint64, leaseExpiresAt time.Time, agentVersion string, protocolVersion uint32, capabilities []string, bootID string, startedAt time.Time, appliedRevision uint64) error {
	now := time.Now().UTC()
	capabilitiesJSON, err := json.Marshal(capabilities)
	if err != nil {
		return fmt.Errorf("encode node capabilities: %w", err)
	}
	updates := map[string]any{
		"connection_state":  model.ConnectionConnected,
		"lease_expires_at":  leaseExpiresAt.UTC(),
		"agent_version":     agentVersion,
		"protocol_version":  strconv.FormatUint(uint64(protocolVersion), 10),
		"capabilities_json": string(capabilitiesJSON),
		"last_connected_at": &now,
		"last_heartbeat":    &now,
		"last_boot_id":      bootID,
	}
	if !startedAt.IsZero() {
		startedAt = startedAt.UTC()
		updates["last_started_at"] = &startedAt
	}
	if appliedRevision > 0 {
		updates["applied_revision"] = appliedRevision
	}
	result := s.db.Model(&model.MailServer{}).
		Where("id = ? AND enrollment_state = ?", serverID, model.EnrollmentApproved).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("approved node server %d not found", serverID)
	}
	return nil
}

func (s *Store) UpdateNodeControlHeartbeat(serverID uint64, leaseExpiresAt time.Time, load int, readiness string, appliedRevision uint64, lastApplyError, bootID string) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"connection_state": model.ConnectionConnected,
		"lease_expires_at": leaseExpiresAt.UTC(),
		"last_heartbeat":   &now,
		"current_load":     load,
		"readiness_state":  readiness,
		"last_apply_error": lastApplyError,
		"last_boot_id":     bootID,
	}
	if appliedRevision > 0 {
		updates["applied_revision"] = appliedRevision
	}
	switch readiness {
	case model.ReadinessReady:
		updates["status"] = gorm.Expr("CASE WHEN transport_mode = ? THEN ? ELSE status END", model.TransportControlStream, "healthy")
	case model.ReadinessDegraded:
		updates["status"] = gorm.Expr("CASE WHEN transport_mode = ? THEN ? ELSE status END", model.TransportControlStream, "degraded")
	case model.ReadinessFailed:
		updates["status"] = gorm.Expr("CASE WHEN transport_mode = ? THEN ? ELSE status END", model.TransportControlStream, "down")
	}
	result := s.db.Model(&model.MailServer{}).
		Where("id = ? AND enrollment_state = ?", serverID, model.EnrollmentApproved).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("approved node server %d not found", serverID)
	}
	return nil
}

func (s *Store) UpdateNodeConfigApplied(serverID, revision uint64, succeeded bool, applyError string) error {
	updates := map[string]any{"last_apply_error": applyError}
	if succeeded && revision > 0 {
		updates["applied_revision"] = revision
	}
	return s.db.Model(&model.MailServer{}).Where("id = ?", serverID).Updates(updates).Error
}

func (s *Store) MarkNodeSessionDisconnected(serverID uint64, disconnectedAt time.Time) error {
	return s.db.Model(&model.MailServer{}).Where("id = ?", serverID).Updates(map[string]any{
		"connection_state":     model.ConnectionDisconnected,
		"last_disconnected_at": disconnectedAt.UTC(),
		"status":               gorm.Expr("CASE WHEN transport_mode = ? THEN ? ELSE status END", model.TransportControlStream, "down"),
	}).Error
}

func (s *Store) ExpireNodeControlLeases(now time.Time) error {
	now = now.UTC()
	return s.db.Model(&model.MailServer{}).
		Where("connection_state = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?", model.ConnectionConnected, now).
		Updates(map[string]any{
			"connection_state":     model.ConnectionDisconnected,
			"last_disconnected_at": now,
			"status":               gorm.Expr("CASE WHEN transport_mode = ? THEN ? ELSE status END", model.TransportControlStream, "down"),
		}).Error
}

func (s *Store) RecordNodeSessionAudit(action string, serverID uint64, nodeUUID, sourceIP string, details map[string]any) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return s.db.Create(&model.NodeRegistrationAudit{
		Action: action, EntityType: "server", EntityID: strconv.FormatUint(serverID, 10),
		Actor: "node:" + nodeUUID, SourceIP: sourceIP, Details: string(payload),
	}).Error
}

func (s *Store) UpdateServerTransportWithAudit(server *model.MailServer, nodeUUID, actor, sourceIP, previous, next string) error {
	if server == nil {
		return fmt.Errorf("server is required")
	}
	details, err := json.Marshal(map[string]any{"node_uuid": nodeUUID, "previous_mode": previous, "next_mode": next})
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(server).Error; err != nil {
			return err
		}
		return tx.Create(&model.NodeRegistrationAudit{
			Action: "transport.mode.change", EntityType: "server", EntityID: strconv.FormatUint(server.ID, 10),
			Actor: actor, SourceIP: sourceIP, Details: string(details),
		}).Error
	})
}
