package catalog

import "github.com/KyrieWang7/nous-agent/platform-go/modelconfig"

func (c *Catalog) ModelSettings() (modelconfig.PublicSettings, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	settings, err := modelconfig.Load(c.harnessPath, modelconfig.Path(c.extensionsPath))
	return settings.Public(), err
}

func (c *Catalog) SaveModelSettings(input modelconfig.Input) (modelconfig.PublicSettings, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := modelconfig.Path(c.extensionsPath)
	previous, err := modelconfig.Load(c.harnessPath, path)
	if err != nil {
		return modelconfig.PublicSettings{}, err
	}
	settings, err := modelconfig.Replace(previous, input)
	if err != nil {
		return modelconfig.PublicSettings{}, err
	}
	if err := modelconfig.Write(path, settings); err != nil {
		return modelconfig.PublicSettings{}, err
	}
	return settings.Public(), nil
}
