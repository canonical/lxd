import sys

sys.path.append('./')
from custom_conf import *

# The file contains helper functions and the mechanism to build the
# .sphinx/requirements.txt file that is needed to set up the virtual
# environment.

# You should not do any modifications to this file. Put your custom
# requirements into the custom_required_modules array in the custom_conf.py
# file. If you need to change this file, contribute the changes upstream.

legacyCanonicalSphinxExtensionNames = [
    "youtube-links",
    "related-links",
    "custom-rst-roles",
    "terminal-output"
    ]

def IsAnyCanonicalSphinxExtensionUsed():
    for extension in custom_extensions:
        if (extension.startswith("canonical.") or
            extension in legacyCanonicalSphinxExtensionNames):
            return True

    return False

def IsNotFoundExtensionUsed():
    return "notfound.extension" in custom_extensions

def IsSphinxTabsUsed():
    for extension in custom_extensions:
        if extension.startswith("sphinx_tabs."):
            return True

    return False

def AreRedirectsDefined():
    return ("sphinx_reredirects" in custom_extensions) or (
            ("redirects" in globals()) and \
            (redirects is not None) and \
            (len(redirects) > 0))

def IsOpenGraphConfigured():
    if "sphinxext.opengraph" in custom_extensions:
        return True

    for global_variable_name in list(globals()):
        if global_variable_name.startswith("ogp_"):
            return True

    return False

def IsMyStParserUsed():
    return ("myst_parser" in custom_extensions) or \
           ("custom_myst_extensions" in globals())

def DeduplicateExtensions(extensionNames: [str]):
    extensionNames = dict.fromkeys(extensionNames)
    resultList = []
    encounteredCanonicalExtensions = []

    for extensionName in extensionNames:
        if extensionName in legacyCanonicalSphinxExtensionNames:
            extensionName = "canonical." + extensionName

        if extensionName.startswith("canonical."):
            if extensionName not in encounteredCanonicalExtensions:
                encounteredCanonicalExtensions.append(extensionName)
                resultList.append(extensionName)
        else:
            resultList.append(extensionName)

    return resultList

if __name__ == "__main__":
    requirements = [
        "furo",
        "pyspelling",
        "sphinx",
        "sphinx-autobuild",
        "sphinx-copybutton",
        "sphinx-design",
        "sphinxcontrib-jquery",
        "watchfiles",
        "GitPython"

    ]

    requirements.extend(custom_required_modules)

    if IsAnyCanonicalSphinxExtensionUsed():
        requirements.append("canonical-sphinx-extensions")

    if IsNotFoundExtensionUsed():
        requirements.append("sphinx-notfound-page")

    if IsSphinxTabsUsed():
        requirements.append("sphinx-tabs")

    if AreRedirectsDefined():
        requirements.append("sphinx-reredirects")

    if IsOpenGraphConfigured():
        requirements.append("sphinxext-opengraph")

    if IsMyStParserUsed():
        requirements.append("myst-parser")
        requirements.append("linkify-it-py")

    # removes duplicate entries
    requirements = list(dict.fromkeys(requirements))
    requirements.sort()

    with open(".sphinx/requirements.txt", 'w') as requirements_file:
        requirements_file.write(
            "# DO NOT MODIFY THIS FILE DIRECTLY!\n"
            "#\n"
            "# This file is generated automatically.\n"
            "# Add custom requirements to the custom_required_modules\n"
            "# array in the custom_conf.py file and run:\n"
            "# make clean && make install\n")

        for requirement in requirements:
            requirements_file.write(requirement)
            requirements_file.write('\n')
