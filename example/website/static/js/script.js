setInterval(function() {
    let val = parseInt(document.getElementById("counter").innerHTML);
    val += 1;
    document.getElementById("counter").innerHTML = val.toString();
}, 1000);