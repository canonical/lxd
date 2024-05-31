# This Makefile stub allows you to customize starter pack (SP) targets.
# Consider this file as a bridge between your project
# and the starter pack's predefined targets that reside in Makefile.sp.
#
# You can add your own, non-SP targets here or override SP targets
# to fit your project's needs. For example, you can define and use targets
# named "install" or "run", but continue to use SP targets like "sp-install"
# or "sp-run" when working on the documentation.

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
        "* other possible targets:                    make <TAB twice> \n" \
        "------------------------------------------------------------- \n"

%:
	$(MAKE) -f Makefile.sp sp-$@
