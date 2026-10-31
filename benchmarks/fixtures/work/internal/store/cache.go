package store

// Cache holds the values a Store wrote.
type Cache struct {
	entries map[string]string
}

func NewCache() *Cache { return &Cache{entries: map[string]string{}} }

func (c *Cache) Put(key, value string) {
	c.entries[key] = value
}
