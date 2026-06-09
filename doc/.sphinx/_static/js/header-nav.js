$(document).ready(function() {
    $(document).on("click", function () {
        $(".more-links-dropdown").hide();
    });

    $('.nav-more-links').click(function(event) {
        $('.more-links-dropdown').toggle();
        event.stopPropagation();
    });
})
