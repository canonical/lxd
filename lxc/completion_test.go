package main

import (
	"bytes"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBashCompletionFallbackPreamble(t *testing.T) {
	app := &cobra.Command{Use: "lxc"}
	setupBashCompletion(app)

	buf := new(bytes.Buffer)
	app.SetOut(buf)
	app.SetArgs([]string{"completion", "bash"})

	err := app.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "_init_completion()")
	assert.Contains(t, out, "_get_comp_words_by_ref()")
	assert.Contains(t, out, "_filedir()")
	assert.Contains(t, out, "__start_lxc")
	assert.Contains(t, out, "__complete")
}

func TestBashCompletionNoDescriptions(t *testing.T) {
	app := &cobra.Command{Use: "lxc"}
	setupBashCompletion(app)

	buf := new(bytes.Buffer)
	app.SetOut(buf)
	app.SetArgs([]string{"completion", "bash", "--no-descriptions"})

	err := app.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "_get_comp_words_by_ref()")
	assert.Contains(t, out, "__completeNoDesc")
}

func TestBashCompletionInSubshell(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Bash completion subshell tests are Linux-specific")
	}

	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	app := &cobra.Command{Use: "lxc"}
	setupBashCompletion(app)

	buf := new(bytes.Buffer)
	app.SetOut(buf)
	app.SetArgs([]string{"completion", "bash"})

	err = app.Execute()
	require.NoError(t, err)
	script := buf.String()

	t.Run("core26 environment with bash-completion 2.12+", func(t *testing.T) {
		// Simulates core26 where _comp_initialize and _comp_get_words exist, but _init_completion is undefined.
		driver := `
compopt() {
    return 0
}

_comp_initialize() {
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev=""
    if [[ $COMP_CWORD -gt 0 ]]; then
        prev="${COMP_WORDS[$((COMP_CWORD - 1))]}"
    fi
    words=("${COMP_WORDS[@]}")
    cword=$COMP_CWORD
    return 0
}

_comp_get_words() {
    cur="${COMP_WORDS[COMP_CWORD]}"
    words=("${COMP_WORDS[@]}")
    cword=$COMP_CWORD
    return 0
}

_comp_compgen() {
    return 0
}

source <(cat)

if ! declare -F _init_completion >/dev/null 2>&1; then
    echo "_init_completion not defined" >&2
    exit 1
fi

if ! declare -F _get_comp_words_by_ref >/dev/null 2>&1; then
    echo "_get_comp_words_by_ref not defined" >&2
    exit 1
fi

if ! declare -F _filedir >/dev/null 2>&1; then
    echo "_filedir not defined" >&2
    exit 1
fi

# Simulate invocation
COMP_WORDS=(lxc list "")
COMP_CWORD=2
COMP_LINE="lxc list "
COMP_POINT=9
__start_lxc lxc list "" || true
exit 0
`
		cmd := exec.Command(bashPath, "--noprofile", "--norc", "-e", "-c", driver)
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "subshell failed: %s", string(out))
	})

	t.Run("bare bash without bash-completion", func(t *testing.T) {
		// Simulates environment with no bash-completion package at all.
		driver := `
source <(cat)

if ! declare -F _get_comp_words_by_ref >/dev/null 2>&1; then
    echo "_get_comp_words_by_ref not defined" >&2
    exit 1
fi

# Verify the pure-bash word reassembly fallback
COMP_WORDS=(lxc list "")
COMP_CWORD=2
COMP_LINE="lxc list "
COMP_POINT=9
run_reassembly() {
    local cur prev words cword
    _get_comp_words_by_ref -n =: cur prev words cword || exit 1
    [[ "$cur" == "" ]] || exit 2
    [[ "$prev" == "list" ]] || exit 3
    [[ "$cword" -eq 2 ]] || exit 4
    [[ "${#words[@]}" -eq 3 ]] || exit 5
}

run_reassembly
`
		cmd := exec.Command(bashPath, "--noprofile", "--norc", "-e", "-c", driver)
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "subshell failed: %s", string(out))
	})

	t.Run("existing bash-completion package not overwritten", func(t *testing.T) {
		driver := `
_init_completion() { echo "custom_init"; }
_get_comp_words_by_ref() { echo "custom_words"; }
_filedir() { echo "custom_filedir"; }

source <(cat)

[[ "$(_init_completion)" == "custom_init" ]] || exit 1
[[ "$(_get_comp_words_by_ref)" == "custom_words" ]] || exit 2
[[ "$(_filedir)" == "custom_filedir" ]] || exit 3
`
		cmd := exec.Command(bashPath, "--noprofile", "--norc", "-e", "-c", driver)
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "subshell failed: %s", string(out))
	})

	t.Run("snapcraft completer sed transformation", func(t *testing.T) {
		// Simulates the exact snapcraft pipeline from snap/snapcraft.yaml and executes
		// the resulting completer script in a core26-like environment.
		driver := `
export TERM="${TERM:-dumb}"

tput() {
    echo 80
}

set_cmds='s/^\s*complete.*__start_lxc /&lxd.lxc /'
set_cols='s/# $COLUMNS.*/COLUMN="$(tput cols)"  \# store the current shell width./'
set_compopt='s|$(type -t compopt)|"builtin"|'
set_request_comp='s|requestComp="${words\[0\]} __complete ${args\[\*\]}"|requestComp="/snap/lxd/current/commands/lxc __complete ${args[*]}"|'

transformed=$(sed -e "${set_cmds}" -e "${set_cols}" -e "${set_compopt}" -e "${set_request_comp}")

# Verify snapcraft transformations applied
echo "$transformed" | grep -F "lxd.lxc" >/dev/null || exit 1
echo "$transformed" | grep -F "/snap/lxd/current/commands/lxc" >/dev/null || exit 2

# Verify fallback is still present
echo "$transformed" | grep -F "_get_comp_words_by_ref()" >/dev/null || exit 3
echo "$transformed" | grep -F "_init_completion()" >/dev/null || exit 4

# Now simulate core26 execution inside snap container
_comp_initialize() {
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev=""
    if [[ $COMP_CWORD -gt 0 ]]; then
        prev="${COMP_WORDS[$((COMP_CWORD - 1))]}"
    fi
    words=("${COMP_WORDS[@]}")
    cword=$COMP_CWORD
    return 0
}

_comp_get_words() {
    cur="${COMP_WORDS[COMP_CWORD]}"
    words=("${COMP_WORDS[@]}")
    cword=$COMP_CWORD
    return 0
}

_comp_compgen() {
    return 0
}

eval "$transformed"

# Verify _init_completion was defined by the fallback
declare -F _init_completion >/dev/null || exit 5
declare -F _get_comp_words_by_ref >/dev/null || exit 6

# Execute completion for lxd.lxc as snapd does
COMP_WORDS=(lxd.lxc list "")
COMP_CWORD=2
COMP_LINE="lxd.lxc list "
COMP_POINT=13
__start_lxc lxd.lxc list "" || true
`
		cmd := exec.Command(bashPath, "--noprofile", "--norc", "-e", "-c", driver)
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "subshell failed: %s", string(out))
	})
}
