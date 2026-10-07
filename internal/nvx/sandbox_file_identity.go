package nvx

// directoryLocation is where a directory a grant was recorded for is now.
type directoryLocation int

const (
	// locationUnknown means the question could not be answered: no identity was
	// recorded, the volume gives none, or the lookup failed for a reason that does
	// not say the directory is gone.
	locationUnknown directoryLocation = iota
	// locationHere means the directory exists, at the path returned, which is not
	// always the path that was recorded.
	locationHere
	// locationGone means the directory no longer exists, so a permission on it
	// went with it.
	locationGone
)

// locateDirectory is locateGrantedDirectory, held in a variable so a test can say
// where a directory went without a filesystem that has file IDs.
var locateDirectory = locateGrantedDirectory
