/* JavaScript for the _templates/variant-selector.html file, implementing
 * the version switcher for the documentation.
 *
 * The script gets available versions from the versions.json file on the
 * master branch (because the master branch contains the current information
 * on which versions we want to display).
 * It then links to other versions of the documentation - to the same page
 * if the page is available or to the index otherwise.
 */

// Link to the versions.json file on the master branch.
var versionURL = "https://linuxcontainers.org/lxd/docs/master/versions.json";

// URL prefix that is common for the different documentation sets.
var URLprefix = "https://linuxcontainers.org/lxd/docs/"



$(document).ready(function()
{

    // Read the versions.json file and call the listVersions function.
    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function () {
        if (xhr.readyState === 4) {
            if (xhr.status === 200) {
                listVersions(JSON.parse(xhr.responseText));
            }
            else {
                console.log("URL "+versionURL+" cannot be loaded.");
            }
        }
    };
    xhr.open('GET', versionURL, true);
    xhr.send();

});

// Retrieve the name of the current documentation set (for example,
// 'master' or 'stable-5.0') and the path to the page (for example,
// 'howto/pagename/').
function getPaths()
{
    var paths = {};

    var prefix = new URL(URLprefix);
    var url = window.location.pathname;

    if (url.startsWith(prefix.pathname)) {

        path = url.substr(prefix.pathname.length).split("/");
        paths['current'] = path.shift();
        if (paths['current'] == "master") {
            paths['current'] = "latest";
        };
        paths['page'] = path.join("/");
    }
    else {
        console.log("Unexpected hosting URL!");
    }

    return paths;

}

// Populate the version dropdown.
function listVersions(data)
{
    paths = getPaths();

    var all_versions = document.getElementById("all-versions");
    var current = document.getElementById("current");
    for( var i = 0; i < data.length; i++ )
    {
        var one = data[i];
        if (one.id === paths['current']) {
            // Put the current version at the top without link.
            current.innerText = one.version+" ⌄";
        }
        else {
            // Put other versions into the dropdown and link them to the
            // suitable URL.
            var version = document.createElement("a");
            version.appendChild(document.createTextNode(one.version));
            version.href = findNewURL(paths,one.id);
            all_versions.appendChild(version);
        }
    }
}

// Check if the same page exists in the other documentation set.
// If yes, return the new link. Otherwise, link to the index page of
// the other documentation set.
function findNewURL(paths,newset) {

    var newURL = URLprefix.concat(newset,"/",paths['page']);
    var xhr = new XMLHttpRequest();
    xhr.open('HEAD', newURL, false);
    xhr.send();

    if (xhr.status == "404") {
        return URLprefix.concat(newset,"/");
    } else {
        return newURL;
    }

}

// Toggle the version dropdown.
function dropdown() {
  document.getElementById("all-versions").classList.toggle("show");
}

// Close the dropdown menu if the user clicks outside of it.
window.onclick = function(event) {
  if (!event.target.matches('.version_select')) {
    var dropdowns = document.getElementsByClassName("available_versions");
    var i;
    for (i = 0; i < dropdowns.length; i++) {
      var openDropdown = dropdowns[i];
      if (openDropdown.classList.contains('show')) {
        openDropdown.classList.remove('show');
      }
    }
  }
}
