// Package config loads the instance configuration (DESIGN.md §19.1): built-in safe defaults, an
// optional config.yaml, and environment variables, which win over the file. The bot starts with
// no config file and no environment at all. See docs/CONFIGURATION.md for every key.
package config
