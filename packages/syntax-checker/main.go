//go:build js && wasm

package main

import (
	"encoding/json"
	"go/parser"
	"go/scanner"
	"go/token"
	"syscall/js"
)

type parseError struct {
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Message string `json:"message"`
}

func check(_ js.Value, args []js.Value) any {
	if len(args) == 0 {
		b, _ := json.Marshal([]parseError{})
		return string(b)
	}
	src := args[0].String()
	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, "tank.go", src, parser.AllErrors)
	if err == nil {
		b, _ := json.Marshal([]parseError{})
		return string(b)
	}
	var errs []parseError
	if el, ok := err.(scanner.ErrorList); ok {
		for _, e := range el {
			errs = append(errs, parseError{Line: e.Pos.Line, Col: e.Pos.Column, Message: e.Msg})
		}
	} else {
		errs = []parseError{{Line: 1, Col: 1, Message: err.Error()}}
	}
	b, _ := json.Marshal(errs)
	return string(b)
}

func main() {
	done := make(chan struct{})
	js.Global().Set("goSyntaxCheck", js.FuncOf(check))
	<-done
}
