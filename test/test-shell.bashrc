# lxc command completion
if [ -f /etc/profile.d/bash_completion.sh ] && command -v lxc > /dev/null; then
    . /etc/profile.d/bash_completion.sh
    . <(lxc completion bash)
fi

# provide useful aliases like `ll`, etc
if [ -f ~/.bashrc ]; then
    . ~/.bashrc
fi

# yellow
export PS1="\[\033[0;33mLXD-TEST\033[0m ${PS1:-\u@\h:\w\$ }\]"
