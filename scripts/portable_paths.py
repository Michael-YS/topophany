"""One pathname policy for source identities and cross-platform archives."""
import unicodedata


def portable_parts(name: str) -> list[str]:
    parts = name.split("/")
    reserved = {"con", "prn", "aux", "nul", "conin$", "conout$"}
    reserved.update(f"{prefix}{digit}" for prefix in ("com", "lpt") for digit in "123456789¹²³")
    if any(c in name for c in '\\:<>"|?*') or any(unicodedata.category(c).startswith("C") for c in name):
        raise RuntimeError("nonportable source path: " + repr(name))
    for part in parts:
        if not part or part in {".", ".."} or part.endswith((".", " ")) or part.split(".", 1)[0].rstrip(" ").casefold() in reserved:
            raise RuntimeError("nonportable source path: " + repr(name))
    return parts


def validate_paths(names) -> None:
    """Reject aliases of files or directory prefixes, on every host OS."""
    prefixes = {}
    files = set()
    directories = set()
    for name in names:
        parts = portable_parts(name)
        for end in range(1, len(parts) + 1):
            prefix = "/".join(parts[:end])
            key = unicodedata.normalize("NFC", prefix.upper().casefold())
            if key in prefixes and prefixes[key] != prefix:
                raise RuntimeError("portable path collision: " + repr(name))
            prefixes[key] = prefix
            if end < len(parts):
                if key in files:
                    raise RuntimeError("file/directory path collision: " + repr(name))
                directories.add(key)
            else:
                if key in files or key in directories:
                    raise RuntimeError("duplicate or file/directory path collision: " + repr(name))
                files.add(key)
