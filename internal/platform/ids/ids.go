// Package ids gera identificadores UUIDv7 (ordenáveis por tempo) sem dependências externas.
package ids

import (
	"crypto/rand"
	"fmt"
	"time"

	"desafio/internal/domain"
)

// Generator implementa app.IDGenerator.
type Generator struct{}

// NewID devolve um UUIDv7: 48 bits de milissegundos Unix, versão 7, variante RFC 4122 e bits aleatórios.
func (Generator) NewID() domain.ID {
	var b [16]byte
	ms := uint64(time.Now().UnixMilli())
	b[0], b[1], b[2], b[3], b[4], b[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		panic(fmt.Sprintf("ids: sem entropia: %v", err)) // falha irrecuperável do sistema, não regra de negócio
	}
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return domain.ID(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]))
}
