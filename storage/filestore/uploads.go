package filestore

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// uploadsFile is the name of the original-name map inside the session
// directory.
//
// ★ It sits next to the transcript and the state, **not inside the
//
//	workspace**: the workspace is the directory the *model* reads, and a
//	map from hashed names to the names the user typed means nothing to it.
const uploadsFile = "uploads.json"

// uploadNames maps a workspace-relative path to the name the file carried
// when the user uploaded it.
//
// ★ The path is the key, and the name is the value — the other way round
//
//	would not be a map at all: one name can be uploaded twice, and the two
//	files are different.
type uploadNames struct {
	Version int               `json:"version"`
	Files   map[string]string `json:"files"`
}

// RecordUpload remembers the name a workspace file had before it was
// renamed.
//
// ★ The map is read, changed and written **under the session's lock**, so
//
//	concurrent uploads cannot lose an entry: two files uploaded at the same
//	moment would otherwise both read the same old map and the second write
//	would drop the first file's name.
func (fs *FileSession) RecordUpload(path, originalName string) error {
	if path == "" || originalName == "" {
		return nil
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	names, err := fs.readUploadNames()
	if err != nil {
		return err
	}
	if names.Files == nil {
		names.Files = map[string]string{}
	}
	names.Files[filepath.ToSlash(path)] = originalName
	return fs.writeUploadNames(names)
}

// OriginalNames returns the map from workspace-relative paths to the names
// the files carried when they were uploaded.
//
// ★ It is read **once**: the listing walks every file in the workspace, and
//
//	a lookup per file would read the map once per file with it.
func (fs *FileSession) OriginalNames() map[string]string {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	names, err := fs.readUploadNames()
	if err != nil {
		return map[string]string{}
	}
	return names.Files
}

// forgetUpload drops the entry of a file that no longer exists.
//
// ★ It is called after a delete. An entry that outlives its file would
//
//	otherwise show up in the listing as a name for a file that is gone —
//	and the interface would offer to attach something that cannot be read.
func (fs *FileSession) forgetUpload(path string) {
	if path == "" {
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	names, err := fs.readUploadNames()
	if err != nil {
		return
	}
	if _, ok := names.Files[filepath.ToSlash(path)]; !ok {
		return
	}
	delete(names.Files, filepath.ToSlash(path))
	if err := fs.writeUploadNames(names); err != nil {
		fs.logger().Warn("could not forget the original name of a deleted file",
			"path", path, "err", err)
	}
}

// readUploadNames loads the map. A missing file is an empty map.
//
// ★ The caller holds the lock.
func (fs *FileSession) readUploadNames() (uploadNames, error) {
	empty := uploadNames{Version: formatVersion, Files: map[string]string{}}

	data, err := os.ReadFile(uploadsPath(fs.root, fs.id))
	if err != nil {
		if os.IsNotExist(err) {
			return empty, nil
		}
		return empty, err
	}
	var names uploadNames
	if err := json.Unmarshal(data, &names); err != nil {
		// ★ A map that cannot be read is treated as empty rather than as a
		//   failure: losing a display name is a small thing, and refusing
		//   to start the session over it is a large one.
		return empty, nil
	}
	if names.Files == nil {
		names.Files = map[string]string{}
	}
	return names, nil
}

// writeUploadNames persists the map through tmp -> fsync -> rename.
//
// ★ The rename is atomic, so a reader never sees a half-written map — the
//
//	same discipline the state file uses.
//
// ★ The caller holds the lock.
func (fs *FileSession) writeUploadNames(names uploadNames) error {
	names.Version = formatVersion
	path := uploadsPath(fs.root, fs.id)

	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// uploadsPath is the path of the original-name map.
func uploadsPath(root, id string) string {
	return filepath.Join(sessionDir(root, id), uploadsFile)
}
