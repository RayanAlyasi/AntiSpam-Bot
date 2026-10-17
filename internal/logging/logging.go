package logging

import (
	"io"

	"github.com/rs/zerolog"
)

// Loggers hands out zerolog loggers that write JSON lines with a timestamp to one writer, each
// module at its own level.
type Loggers struct {
	base    zerolog.Logger
	global  zerolog.Level
	modules map[string]zerolog.Level
}

// New returns Loggers writing to w. Modules not named in modules log at global. Module names
// are matched exactly.
//
// zerolog also has a process-wide floor (debug by default) that drops anything below it. New
// lowers that floor when global or a module asks for a more verbose level, so a module set to
// trace logs trace; it never raises it.
func New(w io.Writer, global zerolog.Level, modules map[string]zerolog.Level) *Loggers {
	l := &Loggers{global: global, modules: make(map[string]zerolog.Level, len(modules))}
	floor := global
	for module, lvl := range modules {
		l.modules[module] = lvl
		floor = min(floor, lvl)
	}
	if floor < zerolog.GlobalLevel() {
		zerolog.SetGlobalLevel(floor)
	}
	l.base = zerolog.New(w).With().Timestamp().Logger()
	return l
}

// Base returns the logger at the global level, without a module field.
func (l *Loggers) Base() zerolog.Logger {
	return l.base.Level(l.global)
}

// For returns the logger for module: it carries a "module" field and logs at the module's
// level, or at the global level if the module has none.
func (l *Loggers) For(module string) zerolog.Logger {
	lvl, ok := l.modules[module]
	if !ok {
		lvl = l.global
	}
	return l.base.With().Str("module", module).Logger().Level(lvl)
}
