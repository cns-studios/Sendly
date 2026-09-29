// Signed-out /transfers landing (web/templates/transfers-guest.html).
// Runs the demo loop only while it is on screen, tilts it towards the pointer,
// and on phones shows a sticky sign-up button once the main one has scrolled out of view.
(function(){
    var demo=document.getElementById('tg-demo');
    var cta=document.getElementById('tg-cta');
    var sticky=document.getElementById('tg-sticky');
    // tilt the demo towards the pointer (mouse/trackpad only, not with reduced motion)
    var stage=demo&&demo.querySelector('.tg-stage');
    var calm=window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    if(stage&&!calm&&window.matchMedia('(hover: hover) and (pointer: fine)').matches){
        var frame=0;
        document.addEventListener('pointermove',function(e){
            if(frame)return;
            frame=requestAnimationFrame(function(){
                frame=0;
                var r=stage.getBoundingClientRect();
                // -1..1 relative to the stage centre, damped outside of it
                var x=Math.max(-1,Math.min(1,(e.clientX-(r.left+r.width/2))/(r.width*0.9)));
                var y=Math.max(-1,Math.min(1,(e.clientY-(r.top+r.height/2))/(r.height*0.9)));
                stage.style.setProperty('--ry',(x*5).toFixed(2)+'deg');
                stage.style.setProperty('--rx',(-y*4).toFixed(2)+'deg');
                stage.style.setProperty('--px',(x*8).toFixed(1)+'px');
                stage.style.setProperty('--py',(y*6).toFixed(1)+'px');
            });
        });
        document.documentElement.addEventListener('pointerleave',function(){
            ['--rx','--ry','--px','--py'].forEach(function(v){stage.style.removeProperty(v);});
        });
    }
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
