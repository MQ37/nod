package main

import (
	"context"
	"os"

	"github.com/MQ37/nod/internal/cli"
	_ "github.com/MQ37/nod/internal/hooksintegration"
	_ "github.com/MQ37/nod/internal/modelscli"
	_ "github.com/MQ37/nod/internal/server"
	_ "github.com/MQ37/nod/internal/skill"
)

func main() { os.Exit(cli.Default.Run(context.Background(), cli.ProcessEnv(), os.Args[1:])) }
