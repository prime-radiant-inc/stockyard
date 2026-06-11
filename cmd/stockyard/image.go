// cmd/stockyard/image.go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var imageCmd = &cobra.Command{
	Use:   "image",
	Short: "Manage the daemon's image store",
}

var imageLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List images available on the daemon host",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		images, err := c.ListImages(context.Background())
		if err != nil {
			return err
		}
		if len(images) == 0 {
			fmt.Println("No images found")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "REFERENCE\tDIGEST\tSIZE\tCREATED")
		for _, img := range images {
			created := img.CreatedAt
			if created == "" {
				created = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				img.Reference, shortDigest(img.Digest), img.Size, created)
		}
		w.Flush()
		return nil
	},
}

var (
	imageImportRootfs string
	imageImportKernel string
)

var imageImportCmd = &cobra.Command{
	Use:   "import <name>",
	Short: "Register a rootfs image with the daemon (Firecracker registry)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()
		return c.ImportImage(context.Background(), args[0], imageImportRootfs, imageImportKernel)
	},
}

var imageRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a registered image",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()
		return c.RemoveImage(context.Background(), args[0])
	},
}

// shortDigest trims "sha256:" and truncates for table display.
func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	if d == "" {
		return "-"
	}
	return d
}

func init() {
	imageImportCmd.Flags().StringVar(&imageImportRootfs, "rootfs", "", "Path to the rootfs image on the daemon host (required)")
	imageImportCmd.Flags().StringVar(&imageImportKernel, "kernel", "", "Path to a per-image kernel on the daemon host (default: shared kernel)")
	imageImportCmd.MarkFlagRequired("rootfs")
	imageCmd.AddCommand(imageLsCmd, imageImportCmd, imageRmCmd)
	rootCmd.AddCommand(imageCmd)
}
