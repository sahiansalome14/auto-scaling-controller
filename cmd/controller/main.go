// entry point, punto de control, logging

// Flags:
//	-dry-run  observa, decide y registra,no modifica la infra
//	-once     ejecuta un solo ciclo y termina 


package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)