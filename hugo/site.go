package hugo

import (
	"errors"
	"fmt"
	"github.com/kovansky/midas"
	"html/template"
	"os"
	"os/exec"
	"path"
)

var _ midas.SiteService = (*SiteService)(nil)

type SiteService struct {
	Site midas.Site

	registry midas.RegistryService
}

func NewSiteService(config midas.Site) (midas.SiteService, error) {
	if _, ok := midas.RegistryServices[config.Registry.Type]; !ok {
		return nil, midas.Errorf(midas.ErrSiteConfig, "requested registry type %s does not exit", config.Registry.Type)
	}

	siteService := SiteService{
		Site:     config,
		registry: midas.RegistryServices[config.Registry.Type](config),
	}

	err := siteService.registry.OpenStorage()
	if err != nil {
		err = siteService.registry.CreateStorage()
		if err != nil {
			return nil, err
		}
	}

	return siteService, nil
}

func (s SiteService) GetRegistryService() (midas.RegistryService, error) {
	return s.registry, nil
}

func (s SiteService) BuildSite(useCache bool) error {
	var arg string

	if !useCache {
		arg = "--ignoreCache"
	} else {
		arg = ""
	}

	cmd := exec.Command("hugo", arg)
	cmd.Dir = s.Site.RootDir

	out, err := cmd.Output()
	if err != nil {
		return midas.Errorf(midas.ErrInternal, "hugo build errored: %s\ncommand output: %s", err, out)
	}

	return nil
}

func (s SiteService) CreateEntry(payload midas.Payload) (string, error) {
	// Set archetype path and output directory
	modelName := payload.Metadata()["model"].(string)
	model, _ := s.getModel(modelName)
	archetypePath := model.ArchetypePath
	if !path.IsAbs(archetypePath) {
		archetypePath = path.Join(s.Site.RootDir, archetypePath)
	}
	outputDir := model.OutputDir
	if !path.IsAbs(outputDir) {
		outputDir = path.Join(s.Site.RootDir, outputDir)
	}

	// Check if archetype exists
	if !fileExists(archetypePath) {
		return "", midas.Errorf(midas.ErrSiteConfig, "archetype for model %s does not exist", modelName)
	}
	// Check if output dir exists, attempt to create it if it doesn't
	if !fileExists(outputDir) {
		err := os.Mkdir(outputDir, 0775)
		if err != nil {
			return "", err
		}
	}

	// Format output filename
	title := fmt.Sprintf("%v", payload.Entry()["Title"])
	slug := midas.CreateSlug(title)
	outputPath := path.Join(outputDir, slug+".html")

	// Check if output filename is free
	if fileExists(outputPath) {
		return "", midas.Errorf(midas.ErrInvalid, "output file %s already exists", path.Base(outputPath))
	}

	// Read archetype file
	tmpl, err := template.ParseFiles(archetypePath)
	if err != nil {
		return "", err
	}

	// Create output file
	output, err := os.Create(outputPath)
	defer func(output *os.File) {
		_ = output.Close()
	}(output)

	if err != nil {
		return "", err
	}

	// Parse archetype and write it to output
	err = tmpl.Execute(output, struct {
		Entry map[string]interface{}
	}{payload.Entry()})
	if err != nil {
		return "", err
	}

	// Add entry to registry
	entryId := s.EntryId(payload)

	if err = s.registry.CreateEntry(entryId, outputPath); err != nil {
		return outputPath, err
	}
	if err = s.registry.Flush(); err != nil {
		return outputPath, err
	}

	return outputPath, nil
}

func (s SiteService) UpdateEntry(payload midas.Payload) (string, error) {
	// Set archetype path
	modelName := payload.Metadata()["model"].(string)
	model, _ := s.getModel(modelName)
	archetypePath := model.ArchetypePath
	if !path.IsAbs(archetypePath) {
		archetypePath = path.Join(s.Site.RootDir, archetypePath)
	}

	// Check if archetype exists
	if !fileExists(archetypePath) {
		return "", midas.Errorf(midas.ErrSiteConfig, "archetype for model %s does not exist", modelName)
	}

	// Get old path
	entryId := s.EntryId(payload)
	oldPath, _ := s.registry.ReadEntry(entryId)
	outputDir := path.Dir(oldPath)

	// Check if output dir exists, attempt to create it if it doesn't
	if !fileExists(outputDir) {
		err := os.Mkdir(outputDir, 0775)
		if err != nil {
			return "", err
		}
	}

	// Format new output filename
	title := fmt.Sprintf("%v", payload.Entry()["Title"])
	slug := midas.CreateSlug(title)
	outputPath := path.Join(outputDir, slug+".html")

	// Check if output filename is free (excluding situation where name doesn't changed)
	if fileExists(outputPath) && path.Base(outputPath) != path.Base(oldPath) {
		return "", midas.Errorf(midas.ErrInvalid, "output file %s already exists", path.Base(outputPath))
	}

	// Remove old entry if exists
	if fileExists(oldPath) {
		_ = os.Remove(oldPath)
	}

	// Read archetype file
	tmpl, err := template.ParseFiles(archetypePath)
	if err != nil {
		return "", err
	}

	// Create output file
	output, err := os.Create(outputPath)
	defer func(output *os.File) {
		_ = output.Close()
	}(output)

	if err != nil {
		return "", err
	}

	// Parse archetype and write it to output
	err = tmpl.Execute(output, struct {
		Entry map[string]interface{}
	}{payload.Entry()})
	if err != nil {
		return "", err
	}

	// Update entry in registry
	if err = s.registry.UpdateEntry(entryId, outputPath); err != nil {
		return outputPath, err
	}
	if err = s.registry.Flush(); err != nil {
		return outputPath, err
	}

	return outputPath, nil
}

func (SiteService) RemoveEntry(payload midas.Payload) (string, error) {
	// TODO implement me
	panic("implement me")
}

func (s SiteService) EntryId(payload midas.Payload) string {
	return fmt.Sprintf("%s-%d", payload.Metadata()["model"].(string), payload.Entry()["id"].(int))
}

// getModel returns a model from any type (collection or single), and true if model is single or false otherwise.
func (s SiteService) getModel(model string) (*midas.Model, bool) {
	if m, ok := s.Site.CollectionTypes[model]; ok {
		return &m, false
	} else if m, ok := s.Site.SingleTypes[model]; ok {
		return &m, true
	}

	return nil, true
}

// fileExists return true if path exists or false otherwise
func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !errors.Is(err, os.ErrNotExist)
}
