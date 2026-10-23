"""Select CI site checks only for explicit site changes or a manual opt-in."""
import json
import os
from pathlib import Path, PurePosixPath
import subprocess


def site_changed(event_name, event, root=None):
    if event_name == "workflow_dispatch":
        return event.get("inputs", {}).get("site") in (True, "true")
    if event_name == "pull_request":
        pr = event["pull_request"]
        revisions = [f"{pr['base']['sha']}...{pr['head']['sha']}"]
    elif event_name == "push":
        base = event["before"]
        if base == "0" * 40:
            base = subprocess.check_output(
                ["git", "hash-object", "-w", "-t", "tree", "--stdin"],
                input=b"", cwd=root).decode().strip()
        revisions = [base, event["after"]]
    else:
        raise ValueError(f"Unsupported CI event: {event_name}")
    # Disable rename detection so moving a file out of site/ counts as a
    # deletion. NUL delimiters also preserve spaces and newlines in filenames.
    names = subprocess.check_output(
        ["git", "diff", "--name-only", "--no-renames", "-z", *revisions, "--", "site/"],
        cwd=root)
    for name in names.split(b"\0"):
        if not name:
            continue
        path = PurePosixPath(os.fsdecode(name))
        if (path.suffix not in {".md", ".mdx", ".rst", ".adoc"}
                and not path.name.startswith("README")):
            return True
    return False


def main():
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    changed = str(site_changed(os.environ["GITHUB_EVENT_NAME"], event)).lower()
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        output.write(f"changed={changed}\n")
    print(f"Site build and consistency checks: {changed}")


if __name__ == "__main__":
    main()
