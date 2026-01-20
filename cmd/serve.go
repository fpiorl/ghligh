/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/prepuzio/ghligh/document"
	"github.com/spf13/cobra"
)

type importFileResult struct {
	File     string `json:"file"`
	Imported int    `json:"imported"`
	Saved    bool   `json:"saved"`
	Error    string `json:"error,omitempty"`
}

type importSummary struct {
	Files         []importFileResult `json:"files"`
	TotalImported int                `json:"totalImported"`
}

func scanPDFs(root string) ([]string, error) {
	var pdfs []string
	walkFn := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(d.Name()) != ".pdf" {
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		pdfs = append(pdfs, abs)
		return nil
	}

	if err := filepath.WalkDir(root, walkFn); err != nil {
		return nil, err
	}
	return pdfs, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func serveExportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pdfs, err := scanPDFs(".")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var exportedDocs []document.GhlighDoc
	for _, path := range pdfs {
		doc, err := document.Open(path)
		if err != nil {
			// Keep it easy: skip unreadable PDFs
			continue
		}
		doc.AnnotsBuffer = doc.GetAnnotsBuffer()
		doc.HashBuffer = doc.HashDoc()
		exportedDocs = append(exportedDocs, *doc)
		doc.Close()
	}

	writeJSON(w, http.StatusOK, exportedDocs)
}

func serveImportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var importedDocs []document.GhlighDoc
	if err := json.Unmarshal(body, &importedDocs); err != nil {
		http.Error(w, fmt.Sprintf("invalid json: %v", err), http.StatusBadRequest)
		return
	}

	byHash := make(map[string]document.AnnotsMap)
	for _, d := range importedDocs {
		if d.HashBuffer == "" || d.AnnotsBuffer == nil {
			continue
		}
		if byHash[d.HashBuffer] == nil {
			byHash[d.HashBuffer] = make(document.AnnotsMap)
		}
		for page, annots := range d.AnnotsBuffer {
			byHash[d.HashBuffer][page] = append(byHash[d.HashBuffer][page], annots...)
		}
	}

	pdfs, err := scanPDFs(".")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	summary := importSummary{}
	for _, path := range pdfs {
		res := importFileResult{File: path}

		doc, err := document.Open(path)
		if err != nil {
			res.Error = err.Error()
			summary.Files = append(summary.Files, res)
			continue
		}

		h := doc.HashDoc()
		am := byHash[h]
		if am == nil {
			doc.Close()
			continue
		}

		imported, err := doc.Import(am)
		res.Imported = imported
		if err != nil {
			res.Error = err.Error()
			doc.Close()
			summary.Files = append(summary.Files, res)
			continue
		}

		if imported > 0 {
			saved, err := doc.Save()
			res.Saved = saved
			if err != nil {
				res.Error = err.Error()
			}
		}

		doc.Close()
		summary.TotalImported += imported
		summary.Files = append(summary.Files, res)
	}

	writeJSON(w, http.StatusOK, summary)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "serve http import/export endpoints",
	Long: `
	ghligh serve [--addr :8080]

	Starts a simple HTTP server with:
	- POST /export : export highlights recursively under cwd
	- POST /import : import highlights (export JSON format) into PDFs under cwd
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, err := cmd.Flags().GetString("addr")
		if err != nil {
			return err
		}

		mux := http.NewServeMux()
		mux.HandleFunc("/export", serveExportHandler)
		mux.HandleFunc("/import", serveImportHandler)

		srv := &http.Server{Addr: addr, Handler: mux}
		fmt.Fprintf(os.Stderr, "listening on %s\n", addr)
		return srv.ListenAndServe()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().String("addr", ":6969", "http listen address")
}
