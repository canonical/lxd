# shellcheck shell=bash

# Derived from: https://github.com/spf13/cobra/blob/main/bash_completionsV2.go
# Modifications made by Canonical Ltd, 2024
# Original work is licensed under the Apache License, Version 2.0

# bash completion V2 for lxc
# This script provides bash completions for the LXD Snap. Snap completions are achieved by a marshalling script in snapd. Completion requests are intercepted and scanned before serialization by complete.sh, sent to etelpmoc.sh for de-serialization and processing, and finally, results are serialized. See https://github.com/canonical/snapd/blob/master/data/completion/bash/etelpmoc.sh and https://github.com/canonical/snapd/blob/master/data/completion/bash/complete.sh for detailed descriptions on the snapd bash completion process.

__lxc_debug()
{
    if [[ -n ${BASH_COMP_DEBUG_FILE-} ]]; then
        echo "$*" >> "${BASH_COMP_DEBUG_FILE}"
    fi
}

# This function calls the lxc program to obtain the completion
# results and the directive.  It fills the 'out' and 'directive' vars.
__lxc_get_completion_results() {
    local requestComp lastParam lastChar args

    # Prepare the command to request completions for the program.
    # Calling ${words[0]} instead of directly lxc allows handling aliases
    args=("${words[@]:1}")
    # Request completions from the LXC binary in LXD's Snap.
    requestComp="/snap/lxd/current/bin/lxc __complete ${args[*]}"

    lastParam=${words[$((${#words[@]}-1))]}
    lastChar=${lastParam:$((${#lastParam}-1)):1}
    __lxc_debug "lastParam ${lastParam}, lastChar ${lastChar}"

    if [[ -z ${cur} && ${lastChar} != = ]]; then
        # If the last parameter is complete (there is a space following it)
        # We add an extra empty parameter so we can indicate this to the go method.
        __lxc_debug "Adding extra empty parameter"
        requestComp="${requestComp} ''"
    fi

    # When completing a flag with an = (e.g., lxc -n=<TAB>)
    # bash focuses on the part after the =, so we need to remove
    # the flag part from $cur
    if [[ ${cur} == -*=* ]]; then
        cur="${cur#*=}"
   fi

    __lxc_debug "Calling ${requestComp}"
    # Use eval to handle any environment variables and such
    out=$(eval "${requestComp}" 2>/dev/null)

    # Extract the directive integer at the very end of the output following a colon (:)
    directive=${out##*:}
    # Remove the directive
    out=${out%:*}
    if [[ ${directive} == "${out}" ]]; then
        # There is not directive specified
        directive=0
    fi
    __lxc_debug "The completion directive is: ${directive}"
    __lxc_debug "The completions are: ${out}"
}

__lxc_process_completion_results() {
    local shellCompDirectiveError=1
    local shellCompDirectiveNoSpace=2
    local shellCompDirectiveNoFileComp=4
    local shellCompDirectiveFilterFileExt=8
    local shellCompDirectiveFilterDirs=16
    local shellCompDirectiveKeepOrder=32

    if (((directive & shellCompDirectiveError) != 0)); then
        # Error code.  No completion.
        __lxc_debug "Received error from custom completion go code"
        return
    else
        if (((directive & shellCompDirectiveNoSpace) != 0)); then
            __lxc_debug "Activating no space"
            # Since we know the nospace completion option is supported in the LXD Snap environment, we can set it after receiving a no space completion directive from Cobra.
            compopt -o nospace
        fi
        if (((directive & shellCompDirectiveKeepOrder) != 0)); then
            # no sort isn't supported for bash less than < 4.4
            if [[ ${BASH_VERSINFO[0]} -lt 4 || ( ${BASH_VERSINFO[0]} -eq 4 && ${BASH_VERSINFO[1]} -lt 4 ) ]]; then
                __lxc_debug "No sort directive not supported in this version of bash"
            else
                __lxc_debug "Activating keep order"
                compopt -o nosort
            fi
        fi
        if (((directive & shellCompDirectiveNoFileComp) != 0)); then
            __lxc_debug "Activating no file completion"
            compopt +o default
        fi
    fi

    # Separate activeHelp from normal completions
    local completions=()
    local activeHelp=()
    __lxc_extract_activeHelp

    if (((directive & shellCompDirectiveFilterFileExt) != 0)); then
        # File extension filtering
        local fullFilter filter filteringCmd

        # Do not use quotes around the $completions variable or else newline
        # characters will be kept.
        # shellcheck disable=SC2048
        for filter in ${completions[*]}; do
            fullFilter+="$filter|"
        done

        filteringCmd="_filedir $fullFilter"
        __lxc_debug "File filtering command: $filteringCmd"
        $filteringCmd
    elif (((directive & shellCompDirectiveFilterDirs) != 0)); then
        # File completion for directories only

        local subdir
        subdir=${completions[0]}
        if [[ -n $subdir ]]; then
            __lxc_debug "Listing directories in $subdir"
            pushd "$subdir" >/dev/null 2>&1 && _filedir -d && popd >/dev/null 2>&1 || return
        else
            __lxc_debug "Listing directories in ."
            _filedir -d
        fi
    else
        __lxc_handle_completion_types
    fi

    __lxc_handle_special_char "$cur" :
    __lxc_handle_special_char "$cur" =

    # Print the activeHelp statements before we finish
    if ((${#activeHelp[*]} != 0)); then
        printf "\n";
        printf "%s\n" "${activeHelp[@]}"
        printf "\n"

        # The prompt format is only available from bash 4.4.
        # We test if it is available before using it.
        # shellcheck disable=SC2034
        if (x=${PS1@P}) 2> /dev/null; then
            printf "%s%s" "${PS1@P}" "${COMP_LINE[@]}"
        else
            # Can't print the prompt.  Just print the
            # text the user had typed, it is workable enough.
            printf "%s" "${COMP_LINE[@]}"
        fi
    fi
}

# Separate activeHelp lines from real completions.
# Fills the $activeHelp and $completions arrays.
__lxc_extract_activeHelp() {
    local activeHelpMarker="_activeHelp_ "
    local endIndex=${#activeHelpMarker}

    while IFS='' read -r comp; do
        if [[ ${comp:0:endIndex} == "$activeHelpMarker" ]]; then
            comp=${comp:endIndex}
            __lxc_debug "ActiveHelp found: $comp"
            if [[ -n $comp ]]; then
                activeHelp+=("$comp")
            fi
        else
            # Not an activeHelp line but a normal completion
            completions+=("$comp")
        fi
    done <<<"${out}"
}

__lxc_handle_completion_types() {
    __lxc_debug "__lxc_handle_completion_types: COMP_TYPE is $COMP_TYPE"

    case $COMP_TYPE in
    37|42)
        # Type: menu-complete/menu-complete-backward and insert-completions
        # If the user requested inserting one completion at a time, or all
        # completions at once on the command-line we must remove the descriptions.
        # https://github.com/spf13/cobra/issues/1508
        local tab=$'\t' comp
        while IFS='' read -r comp; do
            [[ -z $comp ]] && continue
            # Strip any description
            # shellcheck disable=SC2295
            comp=${comp%%$tab*}
            # Only consider the completions that match
            if [[ $comp == "$cur"* ]]; then
                COMPREPLY+=("$comp")
            fi
        done < <(printf "%s\n" "${completions[@]}")
        ;;

    *)
        # Type: complete (normal completion)
        __lxc_handle_standard_completion_case
        ;;
    esac
}

__lxc_handle_standard_completion_case() {
    local tab=$'\t' comp

    # Short circuit to optimize if we don't have descriptions
    if [[ "${completions[*]}" != *$tab* ]]; then
        IFS=$'\n' read -ra COMPREPLY -d '' < <(compgen -W "${completions[*]}" -- "$cur")
        return 0
    fi

    local longest=0
    local compline
    # Look for the longest completion so that we can format things nicely
    while IFS='' read -r compline; do
        [[ -z $compline ]] && continue
        # Strip any description before checking the length
        # shellcheck disable=SC2295
        comp=${compline%%$tab*}
        # Only consider the completions that match
        [[ $comp == "$cur"* ]] || continue
        COMPREPLY+=("$compline")
        if ((${#comp}>longest)); then
            longest=${#comp}
        fi
    done < <(printf "%s\n" "${completions[@]}")

    # If there is a single completion left, remove the description text
    if ((${#COMPREPLY[*]} == 1)); then
        __lxc_debug "COMPREPLY[0]: ${COMPREPLY[0]}"
        comp="${COMPREPLY[0]%%$tab*}"
        __lxc_debug "Removed description from single completion, which is now: ${comp}"
        COMPREPLY[0]=$comp
    else # Format the descriptions
        # shellcheck disable=SC2086
        __lxc_format_comp_descriptions $longest
    fi
}

__lxc_handle_special_char()
{
    local comp="$1"
    local char=$2
    if [[ "$comp" == *${char}* && "$COMP_WORDBREAKS" == *${char}* ]]; then
        # shellcheck disable=SC2295
        local word=${comp%"${comp##*${char}}"}
        local idx=${#COMPREPLY[*]}
        while ((--idx >= 0)); do
            COMPREPLY[idx]=${COMPREPLY[idx]#"$word"}
        done
    fi
}

__lxc_format_comp_descriptions()
{
    local tab=$'\t'
    local comp desc maxdesclength
    local longest=$1

    local i ci
    for ci in ${!COMPREPLY[*]}; do
        comp=${COMPREPLY[ci]}
        # Properly format the description string which follows a tab character if there is one
        if [[ "$comp" == *$tab* ]]; then
            __lxc_debug "Original comp: $comp"
            # shellcheck disable=SC2295
            desc=${comp#*$tab}
            # shellcheck disable=SC2295
            comp=${comp%%$tab*}

            # $$(tput cols) stores the current shell width.
            # Remove an extra 4 because we add 2 spaces and 2 parentheses.
            maxdesclength=$(( $(tput cols) - longest - 4 ))

            # Make sure we can fit a description of at least 8 characters
            # if we are to align the descriptions.
            if ((maxdesclength > 8)); then
                # Add the proper number of spaces to align the descriptions
                for ((i = ${#comp} ; i < longest ; i++)); do
                    comp+=" "
                done
            else
                # Don't pad the descriptions so we can fit more text after the completion
                maxdesclength=$(( $(tput cols) - ${#comp} - 4 ))
            fi

            # If there is enough space for any description text,
            # truncate the descriptions that are too long for the shell width
            if ((maxdesclength > 0)); then
                if ((${#desc} > maxdesclength)); then
                    desc=${desc:0:$(( maxdesclength - 1 ))}
                    desc+="…"
                fi
                comp+="  ($desc)"
            fi
            COMPREPLY[ci]=$comp
            __lxc_debug "Final comp: $comp"
        fi
    done
}

__start_lxc()
{
    # shellcheck disable=SC2034
    local cur prev words cword split

    COMPREPLY=()

    # Call _init_completion from the bash-completion package
    # to prepare the arguments properly
    if declare -F _init_completion >/dev/null 2>&1; then
        _init_completion -n =: || return
    else
        __lxc_init_completion -n =: || return
    fi

    __lxc_debug
    __lxc_debug "========= starting completion logic =========="
    __lxc_debug "cur is ${cur}, words[*] is ${words[*]}, #words[@] is ${#words[@]}, cword is $cword"

    # The user could have moved the cursor backwards on the command-line.
    # We need to trigger completion from the $cword location, so we need
    # to truncate the command-line ($words) up to the $cword location.
    words=("${words[@]:0:$cword+1}")
    __lxc_debug "Truncated words[*]: ${words[*]},"

    local out directive
    __lxc_get_completion_results
    __lxc_process_completion_results
}

# Snap calls lxd.lxc under the hood, so we'll need to provide completions for both lxd.lxc and lxc.
complete -o default -F __start_lxc lxd.lxc lxc

# ex: ts=4 sw=4 et filetype=sh
