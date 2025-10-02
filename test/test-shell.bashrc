# lxc command completion
if [ -f /etc/profile.d/bash_completion.sh ] && command -v lxc > /dev/null; then
    . /etc/profile.d/bash_completion.sh
    . <(lxc completion bash)
fi

# load tab completion for test runner
if [ -f "$(dirname "${BASH_SOURCE[0]}")/main.sh.bash-completion" ]; then
    . "$(dirname "${BASH_SOURCE[0]}")/main.sh.bash-completion"
    echo "Tab completion enabled for ./main.sh (usage: ./main.sh <TAB>)"
fi

# provide useful aliases like `ll`, etc
if [ -f ~/.bashrc ]; then
    . ~/.bashrc
fi

# yellow
export PS1="\[\033[0;33mLXD-TEST\033[0m ${PS1:-\u@\h:\w\$ }\]"
