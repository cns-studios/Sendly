// Signed-out /transfers landing (web/templates/transfers-guest.html).
// Runs the demo loop only while it is on screen, and on phones shows a sticky
// sign-up button once the main one has scrolled out of view.
(function(){
    var demo=document.getElementById('tg-demo');
    var cta=document.getElementById('tg-cta');
    var sticky=document.getElementById('tg-sticky');
    if(!('IntersectionObserver' in window)){
        if(demo)demo.classList.add('is-playing');
        return;
    }
    if(demo){
        new IntersectionObserver(function(entries){
            demo.classList.toggle('is-playing',entries[0].isIntersecting);
        },{threshold:0.25}).observe(demo);
    }
    if(cta&&sticky){
        var ctaPassed=false,footerVisible=false;
        var update=function(){sticky.classList.toggle('is-shown',ctaPassed&&!footerVisible);};
        new IntersectionObserver(function(entries){
            var e=entries[0];
            // only once the button is above the viewport, not before reaching it
            ctaPassed=!e.isIntersecting&&e.boundingClientRect.top<0;
            update();
        }).observe(cta);
        var footer=document.querySelector('.footer');
        if(footer){
            new IntersectionObserver(function(entries){
                footerVisible=entries[0].isIntersecting;
                update();
            }).observe(footer);
        }
    }
})();
