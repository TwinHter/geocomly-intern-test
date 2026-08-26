package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"accountlinker/internal/linker"
	"accountlinker/internal/model"
	"accountlinker/internal/parser"
	"accountlinker/internal/similarity"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "linker:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: linker <link|stream> [flags]")
	}
	switch args[0] {
	case "link":
		return runLink(args[1:], stdout)
	case "stream":
		return runStream(args[1:], stdin, stdout)
	default:
		return fmt.Errorf("unknown command %q; expected link or stream", args[0])
	}
}

func runLink(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("link", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	accountsPath := flags.String("accounts", "", "accounts JSONL path")
	constraintsPath := flags.String("constraints", "", "constraints JSONL path")
	outputPath := flags.String("output", "", "clusters JSON path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *accountsPath == "" || *constraintsPath == "" || *outputPath == "" {
		return errors.New("link requires --accounts, --constraints, and --output")
	}
	state, err := loadState(*accountsPath, *constraintsPath)
	if err != nil {
		return err
	}
	f, err := os.Create(*outputPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(state.Output()); err != nil {
		f.Close()
		return fmt.Errorf("write output: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}
	_ = stdout
	return nil
}

func runStream(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("stream", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	accountsPath := flags.String("accounts", "", "accounts JSONL path")
	constraintsPath := flags.String("constraints", "", "constraints JSONL path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *accountsPath == "" || *constraintsPath == "" {
		return errors.New("stream requires --accounts and --constraints")
	}
	state, err := loadState(*accountsPath, *constraintsPath)
	if err != nil {
		return err
	}

	buffered := bufio.NewWriter(stdout)
	err = parser.ScanAccounts(stdin, func(account model.Account) error {
		assignment, err := state.Add(account)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(assignment)
		if err != nil {
			return err
		}
		if _, err := buffered.Write(append(encoded, '\n')); err != nil {
			return err
		}
		return buffered.Flush()
	})
	if err != nil {
		return fmt.Errorf("process stream: %w", err)
	}
	return buffered.Flush()
}

func loadState(accountsPath, constraintsPath string) (*linker.State, error) {
	accounts, err := parser.LoadAccounts(accountsPath)
	if err != nil {
		return nil, err
	}
	constraints, err := parser.LoadConstraints(constraintsPath)
	if err != nil {
		return nil, err
	}
	return linker.Batch(accounts, constraints, similarity.DefaultConfig())
}
