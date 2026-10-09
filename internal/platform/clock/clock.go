// Package clock fornece o relógio da aplicação.
package clock

import "time"

// System implementa app.Clock: UTC truncado a microssegundos, a precisão do timestamptz do PostgreSQL,
// para que o valor gravado seja idêntico ao lido.
type System struct{}

func (System) Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
