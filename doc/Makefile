# Minimal makefile for Sphinx documentation

# You can set these variables from the command line, and also
# from the environment for the first two.

SPHINXDIR     = .sphinx
SPHINXOPTS    ?= -c . -d $(SPHINXDIR)/.doctrees -j auto
SPHINXBUILD   ?= sphinx-build
SOURCEDIR     = .
BUILDDIR      = _build
MANPAGEDIR    = reference/manpages
VENVDIR       = $(SPHINXDIR)/venv
PA11Y         = $(SPHINXDIR)/node_modules/pa11y/bin/pa11y.js --config $(SPHINXDIR)/pa11y.json
VENV          = $(VENVDIR)/bin/activate
TARGET        = .
REQPDFPACKS   = latexmk fonts-freefont-otf texlive-latex-recommended texlive-latex-extra texlive-fonts-recommended texlive-font-utils texlive-lang-cjk texlive-xetex plantuml xindy tex-gyre dvipng
VALE_CONFIG     = $(SPHINXDIR)/vale.ini
VALEDIR         = $(SPHINXDIR)/venv/lib/python*/site-packages/vale
VOCAB_CANONICAL = $(SPHINXDIR)/styles/config/vocabularies/Canonical
VALE_GLOB       := --glob='!{.sphinx/**,reference/manpages/**}'

# Put it first so that "make" without argument is like "make help".
help:
	@echo "\n" \
        "------------------------------------------------------------- \n" \
        "* watch, build and serve the documentation:  make run \n" \
        "* only build:                                make html \n" \
        "* only serve:                                make serve \n" \
        "* clean built doc files:                     make clean-doc \n" \
        "* clean full environment:                    make clean \n" \
        "* check links:                               make linkcheck \n" \
        "* check spelling:                            make spelling \n" \
        "* check spelling (without building again):   make spellcheck \n" \
        "* check inclusive language:                  make woke \n" \
        "* check accessibility:                       make pa11y \n" \
        "* check style guide compliance:              make vale \n" \
        "* check style guide compliance on target:    make vale TARGET=* \n" \
        "* other possible targets:                    make <TAB twice> \n" \
        "------------------------------------------------------------- \n"

.PHONY: full-help html epub pdf linkcheck spelling spellcheck woke \
        vale pa11y run serve install pa11y-install html-rtd \
        vale-install pdf-prep pdf-prep-force clean clean-doc \
        update lint-md lint pymarkdownlnt-install client objects

full-help: $(VENVDIR)
	@. $(VENV); $(SPHINXBUILD) -M help "$(SOURCEDIR)" "$(BUILDDIR)" $(SPHINXOPTS) $(O)
	@echo "\n\033[1;31mNOTE: This help text shows unsupported targets!\033[0m"
	@echo "Run 'make help' to see supported targets."

# If requirements are updated, venv should be rebuilt and timestamped.
$(VENVDIR):
	@echo "... setting up virtualenv"
	python3 -m venv $(VENVDIR) || { echo "You must install python3-venv before you can build the documentation."; exit 1; }
	. $(VENV); pip install $(PIPOPTS) --require-virtualenv \
	    --upgrade -r requirements.txt \
            --log $(VENVDIR)/pip_install.log
	@test ! -f $(VENVDIR)/pip_list.txt || \
            mv $(VENVDIR)/pip_list.txt $(VENVDIR)/pip_list.txt.bak
	@. $(VENV); pip list --local --format=freeze > $(VENVDIR)/pip_list.txt
	@touch $(VENVDIR)

pa11y-install:
	@type $(PA11Y) >/dev/null 2>&1 || { \
			echo "Installing \"pa11y\" from npm... \n"; \
			mkdir -p $(SPHINXDIR)/node_modules/ ; \
			npm install --prefix $(SPHINXDIR) pa11y; \
		}

pymarkdownlnt-install:
	. $(VENV); test -d $(VENVDIR)/lib/python*/site-packages/pymarkdown || pip install pymarkdownlnt

client:
	cd .. && $(MAKE) -f Makefile client

install: $(VENVDIR) client

run: install
	export LOCAL_SPHINX_BUILD=True && . $(VENV) && sphinx-autobuild -b dirhtml "$(SOURCEDIR)" "$(BUILDDIR)" --ignore '$(SPHINXDIR)/deps/manpages/*' $(SPHINXOPTS)

# Doesn't depend on $(BUILDDIR) to rebuild properly at every run.
# If the target is html-rtd, uses LOCAL_SPHINX_BUILD=False; True otherwise (for target html)
html html-rtd: install
	export LOCAL_SPHINX_BUILD=$(if $(filter html-rtd,$@),False,True) && . $(VENV) && $(SPHINXBUILD) -W --keep-going -b dirhtml "$(SOURCEDIR)" "$(BUILDDIR)" -w $(SPHINXDIR)/warnings.txt $(SPHINXOPTS)

epub: install
	. $(VENV); $(SPHINXBUILD) -b epub "$(SOURCEDIR)" "$(BUILDDIR)" -w $(SPHINXDIR)/warnings.txt $(SPHINXOPTS)

serve: html
	cd "$(BUILDDIR)"; python3 -m http.server --bind 127.0.0.1 8000

clean: clean-doc
	@test ! -e "$(VENVDIR)" -o -d "$(VENVDIR)" -a "$(abspath $(VENVDIR))" != "$(VENVDIR)"
	rm -rf $(VENVDIR)
	rm -rf $(SPHINXDIR)/node_modules/
	rm -rf $(SPHINXDIR)/styles
	rm -rf $(VALE_CONFIG)

clean-doc:
	find reference/manpages/ -name "*.md" -type f -delete
	git clean -fx "$(BUILDDIR)"
	rm -rf $(SPHINXDIR)/.doctrees

linkcheck: install
	. $(VENV) ; $(SPHINXBUILD) -b linkcheck "$(SOURCEDIR)" "$(BUILDDIR)" $(SPHINXOPTS) || { grep --color -F "[broken]" "$(BUILDDIR)/output.txt"; exit 1; }
	exit 0

pa11y: pa11y-install html
	find $(BUILDDIR) -name '*.html' -print0 | xargs -n 1 -0 $(PA11Y)

objects:
	# provide a decoded version of objects.inv to the UI
	. $(VENV) ; cd $(BUILDDIR); python3 -m sphinx.ext.intersphinx 'objects.inv' > objects.inv.txt

lint lint-md: pymarkdownlnt-install
	@echo "Running Markdown linting..."
	@. $(VENV); \
	pymarkdownlnt --config $(SPHINXDIR)/.pymarkdown.json scan \
		--recurse \
		--exclude=./$(SPHINXDIR) \
		--exclude=./$(BUILDDIR) \
		--exclude=./$(MANPAGEDIR)/lxc \
		. \
	&& echo "Passed" \
	|| (echo "Failed"; exit 1)

vale-install: $(VENVDIR)
	@. $(VENV); test -f $(VALE_CONFIG) || python3 $(SPHINXDIR)/get_vale_conf.py
	@echo '.Name=="Canonical.400-Enforce-inclusive-terms"' > $(SPHINXDIR)/styles/woke.filter
	@echo '.Level=="error" and .Name!="Canonical.500-Repeated-words" and .Name!="Canonical.000-US-spellcheck"' > $(SPHINXDIR)/styles/error.filter
	@echo '.Name=="Canonical.000-US-spellcheck"' > $(SPHINXDIR)/styles/spelling.filter
	@. $(VENV); find $(VALEDIR)/vale_bin -size 195c -exec vale --version \;

woke: vale-install
	@trap 'cat $(VOCAB_CANONICAL)/accept_backup.txt > $(VOCAB_CANONICAL)/accept.txt && rm $(VOCAB_CANONICAL)/accept_backup.txt' EXIT; \
	cat $(VOCAB_CANONICAL)/accept.txt > $(VOCAB_CANONICAL)/accept_backup.txt; \
	cat $(SOURCEDIR)/.custom_wordlist.txt >> $(VOCAB_CANONICAL)/accept.txt; \
	echo "Running Vale acceptable term check. To change target, set TARGET= with make command."; \
	. $(VENV); vale --config="$(VALE_CONFIG)" --filter='$(SPHINXDIR)/styles/woke.filter' $(VALE_GLOB) $(TARGET)

vale: vale-install
	@trap 'cat $(VOCAB_CANONICAL)/accept_backup.txt > $(VOCAB_CANONICAL)/accept.txt && rm $(VOCAB_CANONICAL)/accept_backup.txt' EXIT; \
	cat $(VOCAB_CANONICAL)/accept.txt > $(VOCAB_CANONICAL)/accept_backup.txt; \
	cat $(SOURCEDIR)/.custom_wordlist.txt >> $(VOCAB_CANONICAL)/accept.txt; \
	echo "Running Vale. To change target, set TARGET= with make command."; \
	. $(VENV); vale --config="$(VALE_CONFIG)" --filter='$(SPHINXDIR)/styles/error.filter' $(VALE_GLOB) $(TARGET)

spelling: vale-install
	@trap 'cat $(VOCAB_CANONICAL)/accept_backup.txt > $(VOCAB_CANONICAL)/accept.txt && rm $(VOCAB_CANONICAL)/accept_backup.txt' EXIT; \
	cat $(VOCAB_CANONICAL)/accept.txt > $(VOCAB_CANONICAL)/accept_backup.txt; \
	cat $(SOURCEDIR)/.custom_wordlist.txt >> $(VOCAB_CANONICAL)/accept.txt; \
	echo "Running Vale spelling check. To change target, set TARGET= with make command."; \
	. $(VENV); vale --config="$(VALE_CONFIG)" --filter='$(SPHINXDIR)/styles/spelling.filter' $(VALE_GLOB) $(TARGET)

spellcheck: spelling
	@echo "Please note that the \`make spellcheck\` command is being deprecated in favor of \`make spelling\`"

pdf-prep: install
	@for packageName in $(REQPDFPACKS); do (dpkg-query -W -f='$${Status}' $$packageName 2>/dev/null | \
        grep -c "ok installed" >/dev/null && echo "Package $$packageName is installed") && continue || \
        (echo "\nPDF generation requires the installation of the following packages: $(REQPDFPACKS)" && \
        echo "" && echo "Run sudo make pdf-prep-force to install these packages" && echo "" && echo \
        "Please be aware these packages will be installed to your system") && exit 1 ; done

pdf-prep-force:
	apt-get update
	apt-get upgrade -y
	apt-get install --no-install-recommends -y $(REQPDFPACKS)

pdf: pdf-prep
	@. $(VENV); $(SPHINXBUILD) -M latexpdf "$(SOURCEDIR)" "$(BUILDDIR)" $(SPHINXOPTS)
	@rm ./$(BUILDDIR)/latex/front-page-light.pdf || true
	@rm ./$(BUILDDIR)/latex/normal-page-footer.pdf || true
	@find ./$(BUILDDIR)/latex -name "*.pdf" -exec mv -t ./$(BUILDDIR) {} +
	@rm -r $(BUILDDIR)/latex
	@echo "\nOutput can be found in ./$(BUILDDIR)\n"

# Catch-all target: route all unknown targets to Sphinx using the new
# "make mode" option.  $(O) is meant as a shortcut for $(SPHINXOPTS).
%:
	export LOCAL_SPHINX_BUILD=True && . $(VENV) && $(SPHINXBUILD) -M $@ "$(SOURCEDIR)" "$(BUILDDIR)" $(SPHINXOPTS) $(O)