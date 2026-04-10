package audit

import (
	"database/sql"
	"log"
)

var Global *AuditLogger

type AuditEvent struct {
	Source      string
	Payload     string
	ActionTaken string
}

type AuditLogger struct {
	db       *sql.DB
	logChan  chan AuditEvent
	stopChan chan struct{}
}

func NewAuditLogger(db *sql.DB, bufferSize int) *AuditLogger {
	logger := &AuditLogger{
		db:       db,
		logChan:  make(chan AuditEvent, bufferSize),
		stopChan: make(chan struct{}),
	}
	go logger.startWorker()

	return logger
}

func (l *AuditLogger) startWorker() {
	query := `INSERT INTO audit_logs (source, payload, action_taken) VALUES (?, ?, ?)`
	
	stmt, err := l.db.Prepare(query)
	if err != nil {
		log.Printf("AuditLogger: Failed to prepare audit log statement: %v", err)
		return
	}
	defer stmt.Close()

	for {
		select {
		case event := <-l.logChan:
			_, err := stmt.Exec(event.Source, event.Payload, event.ActionTaken)
			if err != nil {
				log.Printf("AuditLogger: Failed to write audit log to database: %v", err)
			}
		case <-l.stopChan:
			return
		}
	}
}

func (l *AuditLogger) LogEvent(source, payload, actionTaken string) {
	event := AuditEvent{
		Source:      source,
		Payload:     payload,
		ActionTaken: actionTaken,
	}
	
	select {
	case l.logChan <- event:
	default:
		log.Println("AuditLogger: Warning - log buffer is full, dropping audit event")
	}
}

func (l *AuditLogger) Close() {
	close(l.stopChan)
}
