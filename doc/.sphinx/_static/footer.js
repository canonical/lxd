$(document).ready(function() {
    $(document).on("click", function () {
        $(".all-contributors").hide();
        $("#overlay").hide();
    });

    $('.display-contributors').click(function(event) {
        $('.all-contributors').toggle();
        $("#overlay").toggle();
        event.stopPropagation();
    });
})
